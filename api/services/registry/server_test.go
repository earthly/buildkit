package earthly_registry_v1 //nolint:revive

import (
	"bytes"
	"testing"
)

var headers = `GET / HTTP/1.1
Host: google.com
User-Agent: curl/7.81.0
Accept: */*
Accept: application/json

`

var headersBad = `BOGUS!
Host: google.com
User-Agent: curl/7.81.0
Accept: */*
Accept: application/json

`

func Test_parseHeader(t *testing.T) {
	r := &bytes.Buffer{}
	r.WriteString(headers)
	method, path, header, err := parseHeader(r)

	if want := "GET"; method != want {
		t.Errorf("expected %s, got %s", want, method)
	}

	if want := "/"; path != want {
		t.Errorf("expected %s, got %s", want, path)
	}

	if err != nil {
		t.Errorf("expected no err, got %v", err)
	}

	if want := 3; len(header) != want {
		t.Errorf("expected %d headers, got %d", want, len(header))
	}

	if want := 2; len(header.Values("Accept")) != want {
		t.Errorf("expected %d values, got %d", want, len(header.Values("Accept")))
	}
}

func Test_parseHeaderBad(t *testing.T) {
	r := &bytes.Buffer{}
	r.WriteString(headersBad)
	_, _, _, err := parseHeader(r)

	if err == nil {
		t.Error("expected err, got nil")
	}
}
