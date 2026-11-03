package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureProvider(t *testing.T, fn func(*http.Request) string) *Providers {
	t.Helper()
	store, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.DB.Close() })
	return &Providers{Store: store, Net: &Network{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fn(r))), Request: r}, nil
	})}}}
}
func Test123CloudProtocolAndLargeIDs(t *testing.T) {
	p := fixtureProvider(t, func(r *http.Request) string {
		if r.Header.Get("platform") != "web" || r.Header.Get("app-version") != "3" {
			t.Error("cloud headers wrong")
		}
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing token")
		}
		switch r.URL.Path {
		case "/b/api/file/list/new":
			if r.URL.Host != "yun.123pan.cn" {
				t.Error("incorrect cloud host")
			}
			if r.URL.Query().Get("Page") == "2" {
				if r.URL.Query().Get("next") != "cursor-A" {
					t.Error("cursor not propagated")
				}
				return `{"code":0,"data":{"InfoList":[],"Next":"-1"}}`
			}
			return `{"code":0,"data":{"InfoList":[{"FileId":9007199254740993,"FileName":"large.bin","Size":42,"Type":0,"Etag":"etag","S3KeyFlag":"flag"}],"Next":"cursor-A"}}`
		case "/api/file/download_info":
			var b map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&b)
			if string(b["fileId"]) != "9007199254740993" || string(b["etag"]) != `"etag"` {
				t.Errorf("cloud download body %s", b)
			}
			return `{"code":0,"data":{"DownloadUrl":"https://cdn.example/large.bin"}}`
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return ""
		}
	})
	p.Store.Put("accounts", "a", Account{ID: "a", Platform: "pan123", Credentials: map[string]string{"token": "fixture-token"}})
	s, e := p.Resolve(context.Background(), "", "", "a", true)
	if e != nil {
		t.Fatal(e)
	}
	v, e := p.Browse(context.Background(), s, "0", 1)
	if e != nil {
		t.Fatal(e)
	}
	if v.Items[0].ID != "9007199254740993" {
		t.Fatal("integer ID precision lost")
	}
	if _, e = p.Browse(context.Background(), s, "0", 2); e != nil {
		t.Fatal(e)
	}
	sources, e := p.Sources(context.Background(), s, v.Items[0])
	if e != nil {
		t.Fatal(e)
	}
	if sources[0].Headers["Authorization"] != "" {
		t.Fatal("token sent to CDN")
	}
}
func TestUCCloudAndShareContracts(t *testing.T) {
	p := fixtureProvider(t, func(r *http.Request) string {
		switch r.URL.Path {
		case "/1/clouddrive/share/sharepage/token":
			return `{"status":200,"data":{"stoken":"token"}}`
		case "/1/clouddrive/transfer_share/detail":
			if r.URL.Query().Get("stoken") != "token" || r.URL.Query().Get("entry") != "ft" {
				t.Error("share token/query wrong")
			}
			return `{"status":200,"data":{"detail_info":{"list":[{"fid":"f","file_name":"test.bin","size":10,"dir":false,"share_fid_token":"fid-token"}]}}}`
		case "/1/clouddrive/file/download":
			if r.URL.RawQuery != "" {
				t.Error("UC cloud download must have no query")
			}
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if len(b) != 1 || b["fids"] == nil {
				t.Error("cloud body has share fields")
			}
			if r.Header.Get("Origin") != "https://drive.uc.cn" {
				t.Error("cloud origin wrong")
			}
			return `{"status":200,"data":[{"download_url":"https://cdn.example/file"}]}`
		default:
			t.Fatal(r.URL)
			return ""
		}
	})
	s, e := p.Resolve(context.Background(), "https://drive.uc.cn/s/example", "", "", false)
	if e != nil {
		t.Fatal(e)
	}
	v, e := p.Browse(context.Background(), s, "0", 1)
	if e != nil {
		t.Fatal(e)
	}
	if !v.More || v.Items[0].Extra["parent"] != "0" {
		t.Fatal("short page wrongly ended or parent missing")
	}
	p.Store.Put("accounts", "a", Account{ID: "a", Platform: "uc", Credentials: map[string]string{"cookie": "fixture=x"}})
	s, e = p.Resolve(context.Background(), "", "", "a", true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Sources(context.Background(), s, v.Items[0]); e != nil {
		t.Fatal(e)
	}
}
func TestMalformedDirectoryNotEmptyAndBiliBool(t *testing.T) {
	p := fixtureProvider(t, func(r *http.Request) string {
		if strings.HasSuffix(r.URL.Path, "nav") {
			return `{"code":0,"data":{"isLogin":true,"uname":"Fixture"}}`
		}
		return `{"status":200,"data":{}}`
	})
	s := &ParseSession{Platform: "quark", Items: map[string]Item{}}
	if _, e := p.Browse(context.Background(), s, "0", 1); e == nil {
		t.Fatal("malformed response accepted as empty")
	}
	a := Account{ID: "a", Platform: "bilibili", Credentials: map[string]string{"cookie": "fixture"}}
	p.Store.Put("accounts", "a", a)
	checked, e := p.CheckAccount(context.Background(), a)
	if e != nil || checked.Status != "valid" || checked.Nickname != "Fixture" {
		t.Fatalf("boolean isLogin parsed incorrectly: %+v", checked)
	}
}
