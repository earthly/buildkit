package filesync

import (
	"bufio"
	"context"
	io "io"
	"os"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
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
	return errors.WithStack(fsutil.Send(stream.Context(), stream, fs, progress))
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

func newStreamMultiWriters(stream grpc.ClientStream, n int) []io.WriteCloser {
	closed := make([]bool, n)
	numClosed := 0
	var closeMu sync.Mutex
	closeFun := func(index int) error {
		closeMu.Lock()
		defer closeMu.Unlock()
		if !closed[index] {
			closed[index] = true
			numClosed++
			if numClosed == n {
				if err := stream.CloseSend(); err != nil {
					return errors.WithStack(err)
				}
				// block until receiver is done
				var bm BytesMessage
				if err := stream.RecvMsg(&bm); err != io.EOF {
					return errors.WithStack(err)
				}
				return nil
			}
		}
		return nil
	}
	bmwcs := make([]io.WriteCloser, 0, n)
	for index := 0; index < n; index++ {
		wc := &streamMultiWriterCloser{
			ClientStream: stream,
			index:        index,
			closeFun:     closeFun,
		}
		bmwc := &bufferedWriteCloser{Writer: bufio.NewWriter(wc), Closer: wc}
		bmwcs = append(bmwcs, bmwc)
	}
	return bmwcs
}

type streamMultiWriterCloser struct {
	grpc.ClientStream
	index    int
	closeFun func(int) error
}

func (wc *streamMultiWriterCloser) Write(dt []byte) (int, error) {
	if err := wc.ClientStream.SendMsg(&BytesMessage{Data: dt, Index: int64(wc.index)}); err != nil {
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

func (wc *streamMultiWriterCloser) Close() error {
	return wc.closeFun(wc.index)
}

func recvDiffCopy(ds grpc.ClientStream, dest string, cu CacheUpdater, progress progressCb, filter func(string, *fstypes.Stat) bool) error {
	st := time.Now()
	defer func() {
		logrus.Debugf("diffcopy took: %v", time.Since(st))
	}()
	var cf fsutil.ChangeFunc
	var ch fsutil.ContentHasher
	if cu != nil {
		cu.MarkSupported(true)
		cf = cu.HandleChange
		ch = cu.ContentHasher()
	}
	return errors.WithStack(fsutil.Receive(ds.Context(), ds, dest, fsutil.ReceiveOpt{
		NotifyHashed:  cf,
		ContentHasher: ch,
		ProgressCb:    progress,
		Filter:        fsutil.FilterFunc(filter),
	}))
}

func syncTargetDiffCopy(ds grpc.ServerStream, dest string) error {
	if err := os.MkdirAll(dest, 0700); err != nil {
		return errors.Wrapf(err, "failed to create synctarget dest dir %s", dest)
	}
	return errors.WithStack(fsutil.Receive(ds.Context(), ds, dest, fsutil.ReceiveOpt{
		Merge: true,
		Filter: func() func(string, *fstypes.Stat) bool {
			uid := os.Getuid()
			gid := os.Getgid()
			return func(p string, st *fstypes.Stat) bool {
				st.Uid = uint32(uid)
				st.Gid = uint32(gid)
				return true
			}
		}(),
	}))
}

func writeTargetFiles(ds grpc.ServerStream, wcs []io.WriteCloser) error {
	for {
		bm := BytesMessage{}
		if err := ds.RecvMsg(&bm); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return errors.WithStack(err)
		}
		index := int(bm.GetIndex())
		if index > len(wcs) {
			return errors.New("index out of bounds")
		}
		wc := wcs[index]
		if _, err := wc.Write(bm.Data); err != nil {
			return errors.WithStack(err)
		}
	}
}
