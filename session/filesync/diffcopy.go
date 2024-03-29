package filesync

import (
	"bufio"
	"context"
	io "io"
	"os"
	"time"

	"github.com/moby/buildkit/util/bklog"

	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"google.golang.org/grpc"
)

type Stream interface {
	Context() context.Context
	SendMsg(m interface{}) error
	RecvMsg(m interface{}) error
}

func sendDiffCopy(stream Stream, fs fsutil.FS, progress progressCb) error {
	return errors.WithStack(fsutil.Send(stream.Context(), stream, fs, progress, nil)) //earthly needs to pass nil
}

func newStreamWriter(stream grpc.ClientStream) io.WriteCloser {
	wc := &streamWriterCloser{ClientStream: stream}
	return &bufferedWriteCloser{Writer: bufio.NewWriter(wc), Closer: wc}
}

type bufferedWriteCloser struct {
	*bufio.Writer
	io.Closer
}

func (bwc *bufferedWriteCloser) Close() error {
	if err := bwc.Writer.Flush(); err != nil {
		return errors.WithStack(err)
	}
	return bwc.Closer.Close()
}

type streamWriterCloser struct {
	grpc.ClientStream
}

func (wc *streamWriterCloser) Write(dt []byte) (int, error) {
	if err := wc.ClientStream.SendMsg(&BytesMessage{Data: dt}); err != nil {
		// SendMsg return EOF on remote errors
		if errors.Is(err, io.EOF) {
			if err := errors.WithStack(wc.ClientStream.RecvMsg(struct{}{})); err != nil {
				return 0, err
			}
		}
		return 0, errors.WithStack(err)
	}
	return len(dt), nil
}

func (wc *streamWriterCloser) Close() error {
	if err := wc.ClientStream.CloseSend(); err != nil {
		return errors.WithStack(err)
	}
	// block until receiver is done
	var bm BytesMessage
	if err := wc.ClientStream.RecvMsg(&bm); err != io.EOF {
		return errors.WithStack(err)
	}
	return nil
}

func recvDiffCopy(ds grpc.ClientStream, dest string, cu CacheUpdater, progress progressCb, differ fsutil.DiffType, filter func(string, *fstypes.Stat) bool) (err error) {
	st := time.Now()
	defer func() {
		bklog.G(ds.Context()).Debugf("diffcopy took: %v", time.Since(st))
	}()
	var cf fsutil.ChangeFunc
	var ch fsutil.ContentHasher
	if cu != nil {
		cu.MarkSupported(true)
		cf = cu.HandleChange
		ch = cu.ContentHasher()
	}
	defer func() {
		// tracing wrapper requires close trigger even on clean eof
		if err == nil {
			ds.CloseSend()
		}
	}()
	return errors.WithStack(fsutil.Receive(ds.Context(), ds, dest, fsutil.ReceiveOpt{
		NotifyHashed:  cf,
		ContentHasher: ch,
		ProgressCb:    progress,
		Filter:        fsutil.FilterFunc(filter),
		Differ:        differ,
	}))
}

func syncTargetDiffCopy(ds grpc.ServerStream, dest string, verboseProgressCB fsutil.VerboseProgressCB) error {
	if err := os.MkdirAll(dest, 0700); err != nil {
		return errors.Wrapf(err, "failed to create synctarget dest dir %s", dest)
	}
	modTime := time.Now().UnixNano() // earthly-specific
	return errors.WithStack(fsutil.Receive(ds.Context(), ds, dest, fsutil.ReceiveOpt{
		Merge: true,
		Filter: func() func(string, *fstypes.Stat) bool {
			uid := os.Getuid()
			gid := os.Getgid()
			return func(p string, st *fstypes.Stat) bool {
				st.Uid = uint32(uid)
				st.Gid = uint32(gid)
				if st.ModTime == 0 {
					st.ModTime = modTime
				}
				return true
			}
		}(),
		VerboseProgressCb: verboseProgressCB,
	}))
}

func writeTargetFile(ds grpc.ServerStream, wc io.WriteCloser) error {
	for {
		bm := BytesMessage{}
		if err := ds.RecvMsg(&bm); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return errors.WithStack(err)
		}
		if _, err := wc.Write(bm.Data); err != nil {
			return errors.WithStack(err)
		}
	}
}
