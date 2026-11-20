package kwik

import "testing"

// A page shaped like kwik's: the stream URL only exists inside a packed script.
const samplePage = `<html><head><script src="/plyr.js"></script></head><body><video></video>
<script>eval(function(p,a,c,k,e,d){e=function(c){return c.toString(36)};if(!''.replace(/^/,String)){while(c--){d[c.toString(a)]=k[c]||c.toString(a)}k=[function(e){return d[e]}];e=function(){return'\\w+'};c=1};while(c--){if(k[c]){p=p.replace(new RegExp('\\b'+e(c)+'\\b','g'),k[c])}}return p}('0 1=\'2://3.4/5/6.7\';8.9(1);a b=\'c\'',62,13,'const|source|https|cdn|example|hls|file|m3u8|console|log|var|x|hi'.split('|'),0,{}))</script>
</body></html>`

func TestUnpack(t *testing.T) {
	got, err := Unpack(samplePage)
	if err != nil {
		t.Fatal(err)
	}
	want := `const source='https://cdn.example/hls/file.m3u8';console.log(source);var x='hi'`
	if got != want {
		t.Fatalf("unpacked:\n got  %s\n want %s", got, want)
	}
}

func TestExtractM3U8(t *testing.T) {
	got, err := ExtractM3U8(samplePage)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example/hls/file.m3u8" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractM3U8Missing(t *testing.T) {
	if _, err := ExtractM3U8("<html>nothing here</html>"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestUnbase62(t *testing.T) {
	cases := map[string]int{"0": 0, "9": 9, "a": 10, "z": 35, "A": 36, "Z": 61, "10": 62, "1a": 72}
	for in, want := range cases {
		if got, ok := unbase(in, 62); !ok || got != want {
			t.Errorf("unbase(%q) = %d,%v want %d", in, got, ok, want)
		}
	}
	if _, ok := unbase("_x", 62); ok {
		t.Error("underscore should not decode")
	}
}
