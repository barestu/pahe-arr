package hls

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestParseMedia(t *testing.T) {
	text := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:5\n#EXT-X-KEY:METHOD=AES-128,URI=\"k.key\"\n#EXTINF:4,\nseg5.ts\n#EXT-X-KEY:METHOD=AES-128,URI=\"https://k/2.key\",IV=0x0000000000000000000000000000000A\n#EXTINF:4,\n/abs/seg6.ts\n#EXT-X-KEY:METHOD=NONE\nseg7.ts\n"
	pl, v, err := Parse(text, "https://cdn.test/a/b/index.m3u8")
	if err != nil || len(v) != 0 || len(pl.Segments) != 3 {
		t.Fatalf("pl=%+v v=%v err=%v", pl, v, err)
	}
	s := pl.Segments
	if s[0].URI != "https://cdn.test/a/b/seg5.ts" || s[0].Seq != 5 || s[0].Key.URI != "https://cdn.test/a/b/k.key" || s[0].Key.IV != nil {
		t.Errorf("seg0 %+v key %+v", s[0], s[0].Key)
	}
	if s[1].URI != "https://cdn.test/abs/seg6.ts" || s[1].Seq != 6 || s[1].Key.IV[15] != 0x0A {
		t.Errorf("seg1 %+v", s[1])
	}
	if s[2].Key != nil {
		t.Errorf("seg2 should be clear")
	}
}

func TestParseMaster(t *testing.T) {
	text := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=1280x720\nlow/index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2400000,CODECS=\"avc1.64001f,mp4a.40.2\"\nhigh/index.m3u8\n"
	pl, v, err := Parse(text, "https://cdn.test/m.m3u8")
	if err != nil || len(pl.Segments) != 0 || len(v) != 2 || v[1].Bandwidth != 2400000 || v[1].URI != "https://cdn.test/high/index.m3u8" {
		t.Fatalf("pl=%+v v=%+v err=%v", pl, v, err)
	}
}

func TestParseRejectsSampleAES(t *testing.T) {
	if _, _, err := Parse("#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"k\"\nseg.ts\n", "https://x/"); err == nil {
		t.Fatal("expected error")
	}
}

func encrypt(t *testing.T, key, iv, plain []byte) []byte {
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	p := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	b, _ := aes.NewCipher(key)
	out := make([]byte, len(p))
	cipher.NewCBCEncrypter(b, iv).CryptBlocks(out, p)
	return out
}

func TestDownloadDecryptsInOrderWithRetry(t *testing.T) {
	key := []byte("0123456789abcdef")
	segs := [][]byte{append([]byte{0x47}, []byte("first")...), append([]byte{0x47}, []byte("second")...), append([]byte{0x47}, []byte("third")...)}
	var hits429 atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://kwik.test/" {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/index.m3u8":
			w.Write([]byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-KEY:METHOD=AES-128,URI=\"enc.key\"\ns0.ts\ns1.ts\ns2.ts\n"))
		case "/enc.key":
			w.Write(key)
		case "/s0.ts", "/s1.ts", "/s2.ts":
			i := int(r.URL.Path[2] - '0')
			if i == 1 && hits429.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(429)
				return
			}
			iv := make([]byte, 16)
			binary.BigEndian.PutUint64(iv[8:], uint64(i))
			w.Write(encrypt(t, key, iv, segs[i]))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	h := http.Header{}
	h.Set("Referer", "https://kwik.test/")
	f := &Fetcher{Client: srv.Client(), Header: h}
	var buf bytes.Buffer
	var last int
	if err := Download(context.Background(), f, srv.URL+"/index.m3u8", &buf, 3, func(d, _ int) { last = d }); err != nil {
		t.Fatal(err)
	}
	want := bytes.Join(segs, nil)
	if !bytes.Equal(buf.Bytes(), want) || last != 3 || hits429.Load() < 2 {
		t.Fatalf("got %q want %q last=%d 429hits=%d", buf.Bytes(), want, last, hits429.Load())
	}

	if n, err := Probe(context.Background(), f, srv.URL+"/index.m3u8"); err != nil || n != 3 {
		t.Fatalf("probe n=%d err=%v", n, err)
	}

	bad := &Fetcher{Client: srv.Client()}
	if _, err := Probe(context.Background(), bad, srv.URL+"/index.m3u8"); err == nil {
		t.Fatal("missing Referer should fail")
	}
}

func TestDecryptBadKey(t *testing.T) {
	iv := make([]byte, 16)
	ct := encrypt(t, []byte("0123456789abcdef"), iv, []byte("hello"))
	b, _ := aes.NewCipher([]byte("fedcba9876543210"))
	if _, err := decrypt(b, iv, ct); err == nil {
		t.Fatal("wrong key should fail padding check")
	}
}
