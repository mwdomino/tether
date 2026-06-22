package proto

import (
	"reflect"
	"testing"
)

func TestExtractLoopbackPorts(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want []int
	}{
		{"no loopback", "https://example.com/foo", nil},
		{"redirect_uri localhost", "https://idp.example/auth?redirect_uri=http%3A%2F%2Flocalhost%3A8085%2Fcb", []int{8085}},
		{"127.0.0.1", "https://idp/auth?redirect_uri=http%3A%2F%2F127.0.0.1%3A4444%2Fcb", []int{4444}},
		{"ipv6 loopback", "https://idp/auth?redirect_uri=http%3A%2F%2F%5B%3A%3A1%5D%3A9090%2Fcb", []int{9090}},
		{"mixed case localhost", "https://idp/auth?return_to=http%3A%2F%2FLocalHost%3A8085%2F", []int{8085}},
		{"two distinct", "https://idp/auth?r1=http%3A%2F%2Flocalhost%3A8085&r2=http%3A%2F%2F127.0.0.1%3A9090", []int{8085, 9090}},
		{"deduplicate", "https://idp/auth?a=http%3A%2F%2Flocalhost%3A8085&b=http%3A%2F%2F127.0.0.1%3A8085", []int{8085}},
		{"fragment", "https://idp/auth#http://localhost:7777/cb", []int{7777}},
		{"port out of range ignored", "https://idp/auth?redirect_uri=http%3A%2F%2Flocalhost%3A99999%2Fcb", nil},
		{"not a loopback hostname", "https://idp/auth?redirect_uri=http%3A%2F%2Fexample.com%3A8085%2Fcb", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractLoopbackPorts(tc.url)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ExtractLoopbackPorts(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}
