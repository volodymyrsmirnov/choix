package media

import (
	"net/url"
	"testing"
)

const urlsTestID = "0123456789abcdef"

func TestURLs_EscapePath(t *testing.T) {
	got := ThumbURL(urlsTestID, "a b/ü#1.jpg", "thumb", "h")
	want := "/media/thumb/" + urlsTestID + "/a%20b/%C3%BC%231.jpg?tier=thumb&v=h"
	if got != want {
		t.Fatalf("ThumbURL = %q, want %q", got, want)
	}
}

func TestURLs_Shapes(t *testing.T) {
	cases := []struct {
		name, got, want string
	}{
		{"thumb preview tier", ThumbURL(urlsTestID, "Day1/A.JPG", "preview", "abc"),
			"/media/thumb/" + urlsTestID + "/Day1/A.JPG?tier=preview&v=abc"},
		{"thumb default tier", ThumbURL(urlsTestID, "A.JPG", "", "abc"),
			"/media/thumb/" + urlsTestID + "/A.JPG?tier=thumb&v=abc"},
		{"thumb without hash", ThumbURL(urlsTestID, "A.JPG", "thumb", ""),
			"/media/thumb/" + urlsTestID + "/A.JPG?tier=thumb"},
		{"full", FullURL(urlsTestID, "Day 1/clip.mov", "h1"),
			"/media/full/" + urlsTestID + "/Day%201/clip.mov?v=h1"},
		{"full without hash", FullURL(urlsTestID, "clip.mov", ""),
			"/media/full/" + urlsTestID + "/clip.mov"},
		{"full escapes query characters", FullURL(urlsTestID, "Trip ?/100%/a&b.jpg", "x y"),
			"/media/full/" + urlsTestID + "/Trip%20%3F/100%25/a&b.jpg?v=x+y"},
		{"preview", PreviewURL(urlsTestID, 3, 1700000000),
			"/media/preview/" + urlsTestID + "/3?v=1700000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Fatalf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

// The handler sees the URL's decoded path with /media stripped: it must be
// the original relative path again.
func TestURLs_RoundTripThroughParsing(t *testing.T) {
	for _, rel := range []string{"a b/ü#1.jpg", "Trip ?/100%/a&b+c.jpg", "x;y/z:w@v.HEIC", "plain.jpg"} {
		u, err := url.Parse(FullURL(urlsTestID, rel, "h"))
		if err != nil {
			t.Fatalf("parse %q: %v", rel, err)
		}
		want := "/media/full/" + urlsTestID + "/" + rel
		if u.Path != want {
			t.Errorf("path of %q = %q, want %q", rel, u.Path, want)
		}
		if v := u.Query().Get("v"); v != "h" {
			t.Errorf("v of %q = %q", rel, v)
		}
	}
}
