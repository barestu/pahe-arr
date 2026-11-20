// Package hls downloads an HLS stream in Go: playlist, AES-128 keys and segments, decrypted and
// written in order. Ported from barestu/animepahe-browser-dl (MIT), src/lib/hls.ts. Doing this
// in Go, rather than letting ffmpeg fetch over the network, means every request carries exactly
// the headers we choose.
package hls

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Key struct {
	URI string
	IV  []byte // nil: derive from the segment's media sequence number
}

type Segment struct {
	URI string
	Seq int64
	Key *Key // nil: not encrypted
}

type Playlist struct {
	Segments []Segment
	MapURI   string // EXT-X-MAP init section (fMP4), written before the segments
}

type Variant struct {
	URI       string
	Bandwidth int64
}

var attrRe = regexp.MustCompile(`([A-Z0-9-]+)=("[^"]*"|[^,]*)`)

func attrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		out[m[1]] = strings.Trim(m[2], `"`)
	}
	return out
}

func resolve(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}

// Parse reads a media playlist (segments) or a master playlist (variants).
func Parse(text, base string) (*Playlist, []Variant, error) {
	pl := &Playlist{}
	var variants []Variant
	var key *Key
	var seq int64
	var pendingBW int64 = -1

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			bw, _ := strconv.ParseInt(attrs(line)["BANDWIDTH"], 10, 64)
			pendingBW = bw
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			n, err := strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
			if err == nil {
				seq = n
			}
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			a := attrs(line)
			switch a["METHOD"] {
			case "", "NONE":
				key = nil
			case "AES-128":
				u, err := resolve(base, a["URI"])
				if err != nil || a["URI"] == "" {
					return nil, nil, fmt.Errorf("bad key URI in %q", line)
				}
				k := &Key{URI: u}
				if iv := a["IV"]; iv != "" {
					b, err := parseIV(iv)
					if err != nil {
						return nil, nil, err
					}
					k.IV = b
				}
				key = k
			default:
				return nil, nil, fmt.Errorf("unsupported encryption %s", a["METHOD"])
			}
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			u, err := resolve(base, attrs(line)["URI"])
			if err != nil {
				return nil, nil, err
			}
			pl.MapURI = u
		case strings.HasPrefix(line, "#"):
		default:
			u, err := resolve(base, line)
			if err != nil {
				return nil, nil, err
			}
			if pendingBW >= 0 {
				variants = append(variants, Variant{URI: u, Bandwidth: pendingBW})
				pendingBW = -1
				continue
			}
			pl.Segments = append(pl.Segments, Segment{URI: u, Seq: seq, Key: key})
			seq++
		}
	}
	return pl, variants, nil
}

func parseIV(s string) ([]byte, error) {
	h := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(h)%2 == 1 {
		h = "0" + h
	}
	b, err := hex.DecodeString(h)
	if err != nil || len(b) > 16 {
		return nil, fmt.Errorf("bad IV %q", s)
	}
	iv := make([]byte, 16)
	copy(iv[16-len(b):], b)
	return iv, nil
}

// Fetcher sends every request with the same headers and retries transient CDN errors.
type Fetcher struct {
	Client   *http.Client
	Header   http.Header
	MaxRetry int // default 6
}

func (f *Fetcher) Get(ctx context.Context, u string) ([]byte, error) {
	maxRetry := f.MaxRetry
	if maxRetry == 0 {
		maxRetry = 6
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range f.Header {
			req.Header[k] = v
		}
		res, err := f.Client.Do(req)
		var wait time.Duration
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var ne net.Error
			if !errors.As(err, &ne) || attempt >= maxRetry {
				return nil, err
			}
		} else {
			body, rerr := io.ReadAll(res.Body)
			res.Body.Close()
			switch {
			case res.StatusCode == http.StatusOK || res.StatusCode == http.StatusPartialContent:
				if rerr != nil {
					return nil, rerr
				}
				return body, nil
			case (res.StatusCode == 429 || res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504) && attempt < maxRetry:
				if s, perr := strconv.Atoi(res.Header.Get("Retry-After")); perr == nil && s > 0 {
					wait = time.Duration(s) * time.Second
				}
			default:
				return nil, &StatusError{URL: u, Status: res.StatusCode}
			}
		}
		if wait == 0 {
			wait = min(time.Second<<attempt, 15*time.Second) + time.Duration(attempt)*250*time.Millisecond
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

type StatusError struct {
	URL    string
	Status int
}

func (e *StatusError) Error() string {
	host := e.URL
	if u, err := url.Parse(e.URL); err == nil {
		host = u.Host
	}
	return fmt.Sprintf("%s returned HTTP %d", host, e.Status)
}

// Load fetches the playlist; for a master playlist it follows the highest-bandwidth variant.
func Load(ctx context.Context, f *Fetcher, u string) (*Playlist, error) {
	for depth := 0; depth < 3; depth++ {
		body, err := f.Get(ctx, u)
		if err != nil {
			return nil, err
		}
		if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("#EXTM3U")) {
			return nil, errors.New("response is not an HLS playlist")
		}
		pl, variants, err := Parse(string(body), u)
		if err != nil {
			return nil, err
		}
		if len(pl.Segments) > 0 {
			return pl, nil
		}
		if len(variants) == 0 {
			return nil, errors.New("playlist has no segments")
		}
		best := variants[0]
		for _, v := range variants[1:] {
			if v.Bandwidth > best.Bandwidth {
				best = v
			}
		}
		u = best.URI
	}
	return nil, errors.New("too many nested playlists")
}

type keyCache struct {
	f  *Fetcher
	mu sync.Mutex
	m  map[string]cipher.Block
}

// block fetches each key once; parallel workers wait for the first fetch instead of repeating it.
func (c *keyCache) block(ctx context.Context, uri string) (cipher.Block, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.m[uri]; ok {
		return b, nil
	}
	raw, err := c.f.Get(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}
	if len(raw) != 16 {
		return nil, fmt.Errorf("key: expected 16 bytes, got %d", len(raw))
	}
	b, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	c.m[uri] = b
	return b, nil
}

func (c *keyCache) segment(ctx context.Context, s Segment) ([]byte, error) {
	data, err := c.f.Get(ctx, s.URI)
	if err != nil {
		return nil, err
	}
	if s.Key == nil {
		return data, nil
	}
	block, err := c.block(ctx, s.Key.URI)
	if err != nil {
		return nil, err
	}
	iv := s.Key.IV
	if iv == nil {
		iv = make([]byte, 16)
		binary.BigEndian.PutUint64(iv[8:], uint64(s.Seq))
	}
	return decrypt(block, iv, data)
}

func decrypt(block cipher.Block, iv, data []byte) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("encrypted segment has invalid length %d", len(data))
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	n := int(out[len(out)-1])
	if n == 0 || n > aes.BlockSize {
		return nil, errors.New("decryption failed (bad padding): wrong key or IV")
	}
	for _, b := range out[len(out)-n:] {
		if int(b) != n {
			return nil, errors.New("decryption failed (bad padding): wrong key or IV")
		}
	}
	return out[:len(out)-n], nil
}

// Download writes the decrypted stream to w in order, fetching up to `concurrency` segments
// at once. Memory stays bounded to roughly `concurrency` segments.
func Download(ctx context.Context, f *Fetcher, m3u8 string, w io.Writer, concurrency int, progress func(done, total int)) error {
	pl, err := Load(ctx, f, m3u8)
	if err != nil {
		return err
	}
	concurrency = max(1, min(concurrency, 4))
	keys := &keyCache{f: f, m: map[string]cipher.Block{}}

	if pl.MapURI != "" {
		init, err := f.Get(ctx, pl.MapURI)
		if err != nil {
			return fmt.Errorf("init section: %w", err)
		}
		if _, err := w.Write(init); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		data []byte
		err  error
	}
	n := len(pl.Segments)
	slots := make([]chan result, n)
	for i := range slots {
		slots[i] = make(chan result, 1)
	}
	sem := make(chan struct{}, concurrency)
	go func() {
		for i := 0; i < n; i++ {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func(i int) {
				data, err := keys.segment(ctx, pl.Segments[i])
				slots[i] <- result{data, err}
			}(i)
		}
	}()

	for i := 0; i < n; i++ {
		var r result
		select {
		case r = <-slots[i]:
		case <-ctx.Done():
			return ctx.Err()
		}
		<-sem
		if r.err != nil {
			return fmt.Errorf("segment %d/%d: %w", i+1, n, r.err)
		}
		if _, err := w.Write(r.data); err != nil {
			return err
		}
		if progress != nil {
			progress(i+1, n)
		}
	}
	return nil
}

// Probe fetches the playlist, the key and the first segment, and checks it decrypts to media.
func Probe(ctx context.Context, f *Fetcher, m3u8 string) (int, error) {
	pl, err := Load(ctx, f, m3u8)
	if err != nil {
		return 0, err
	}
	keys := &keyCache{f: f, m: map[string]cipher.Block{}}
	data, err := keys.segment(ctx, pl.Segments[0])
	if err != nil {
		return 0, fmt.Errorf("first segment: %w", err)
	}
	if pl.MapURI == "" && (len(data) == 0 || data[0] != 0x47) {
		return 0, errors.New("first segment is not MPEG-TS after decryption")
	}
	return len(pl.Segments), nil
}
