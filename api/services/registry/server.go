package earthly_registry_v1 //nolint:revive

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pkg/errors"
)

// NewServer creates and returns a new proxy server with a given host and client.
func NewServer(addr string) *Server {
	return &Server{
		addr: addr,
		cl:   &http.Client{},
	}
}

type Server struct {
	addr string
	cl   *http.Client
	UnimplementedRegistryServer
}

type streamSource interface {
	Send(*ByteMessage) error
	Recv() (*ByteMessage, error)
}

// NewStreamRW creates and returns a gRPC stream reader-writer that implements
// io.Reader & io.Writer as to utilize the gRPC stream with standard methods.
func NewStreamRW(stream streamSource) *StreamRW {
	return &StreamRW{stream: stream}
}

type StreamRW struct {
	stream streamSource
	last   []byte
}

// Write implements io.Writer.
func (s *StreamRW) Write(p []byte) (int, error) {
	err := s.stream.Send(&ByteMessage{
		Data: p,
	})
	if err != nil {
		return 0, errors.Wrap(err, "failed to write data to client")
	}
	return len(p), nil
}

// Read implements io.Reader.
func (s *StreamRW) Read(p []byte) (int, error) {
	l := 0
	if len(s.last) > 0 {
		l = copy(p, s.last)
	}

	msg, err := s.stream.Recv()
	if err != nil {
		return 0, err
	}

	s.last = msg.GetData()
	n := copy(p, s.last)
	s.last = s.last[n:]

	return n + l, nil
}

// parseHeader parses the incoming request header and extracts the method, path,
// & header values.
func parseHeader(r io.Reader) (method string, path string, header http.Header, err error) {
	sc := bufio.NewScanner(r)
	header = http.Header{}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if strings.Contains(line, "HTTP/") {
			parts := strings.Split(line, " ")
			if len(parts) < 2 {
				err = errors.New("invalid status line")
				return
			}
			method, path = parts[0], parts[1]
			continue
		}
		parts := strings.Split(line, ": ")
		if len(parts) < 2 {
			err = errors.New("invalid header format")
			return
		}
		header.Add(parts[0], parts[1])
	}

	err = sc.Err()

	return
}

// Proxy requests sent via gRPC data stream to the embedded Docker registry and
// pipe them back out through the stream again. The rationale is that we have a
// preexisting gRPC connection which can be leveraged to send image data without
// having to support further infrastructure changes.
func (s *Server) Proxy(stream Registry_ProxyServer) error {
	ctx := stream.Context()
	rw := NewStreamRW(stream)

	method, path, header, err := parseHeader(rw)
	if err != nil {
		return err
	}

	addr := strings.ReplaceAll(s.addr, "0.0.0.0", "127.0.0.1")
	u := fmt.Sprintf("http://%s%s", addr, path)

	out, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create request")
	}

	out.Header = header

	res, err := s.cl.Do(out)
	if err != nil {
		return errors.Wrap(err, "failed to send registry request")
	}
	defer res.Body.Close()

	err = res.Write(rw)
	if err != nil {
		return errors.Wrap(err, "failed to write response")
	}

	return nil
}
