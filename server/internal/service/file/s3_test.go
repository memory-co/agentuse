package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestS3SignedConditionalPublicationAndNamespaces(t *testing.T) {
	objects := map[string]string{}
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("unsigned S3 request")
		}
		key := r.URL.Path
		if r.URL.Query().Get("list-type") == "2" {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated>`)
			for p, b := range objects {
				p = strings.TrimPrefix(p, "/bucket/")
				if strings.HasPrefix(p, r.URL.Query().Get("prefix")) {
					fmt.Fprintf(w, "<Contents><Key>%s</Key><Size>%d</Size></Contents>", p, len(b))
				}
			}
			fmt.Fprint(w, "</ListBucketResult>")
			return
		}
		switch r.Method {
		case "PUT":
			if _, ok := objects[key]; ok && r.Header.Get("If-None-Match") == "*" {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(412)
				fmt.Fprint(w, "<Error><Code>PreconditionFailed</Code></Error>")
				return
			}
			b, _ := io.ReadAll(r.Body)
			objects[key] = string(b)
		case "GET":
			b, ok := objects[key]
			if !ok {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(404)
				fmt.Fprint(w, "<Error><Code>NoSuchKey</Code></Error>")
				return
			}
			fmt.Fprint(w, b)
		case "DELETE":
			delete(objects, key)
			w.WriteHeader(204)
		default:
			t.Error(r.Method)
			w.WriteHeader(405)
		}
	}))
	defer ts.Close()
	s, e := NewS3(S3Config{Endpoint: ts.URL, Bucket: "bucket", Prefix: "prefix", AccessKey: "test", SecretKey: "test"})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = s.Put(ctx, "skills", "a/v/skill.zip", strings.NewReader("immutable"), true); e != nil {
		t.Fatal(e)
	}
	if e = s.Put(ctx, "skills", "a/v/skill.zip", strings.NewReader("bad"), true); !errors.Is(e, ErrExists) {
		t.Fatal(e)
	}
	stream, _, e := s.Get(ctx, "skills", "a/v/skill.zip")
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(stream)
	stream.Close()
	if string(data) != "immutable" {
		t.Fatal(string(data))
	}
	rows, e := s.List(ctx, "skills", "a")
	if e != nil || len(rows) != 1 || rows[0].Path != "a/v/skill.zip" {
		t.Fatal(rows, e)
	}
	if e = s.DeletePrefix(ctx, "skills", "a"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Get(ctx, "skills", "a/v/skill.zip"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
