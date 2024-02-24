package pipe

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/pkg/errors"
)

type channel struct {
	OnSendCompletion func()
	value            atomic.Value
	lastValue        *wrappedValue
}

type wrappedValue struct {
	value interface{}
}

func (c *channel) Send(v interface{}) {
	c.value.Store(&wrappedValue{value: v})
	if c.OnSendCompletion != nil {
		c.OnSendCompletion()
	}
}

func (c *channel) Receive() (interface{}, bool) {
	v := c.value.Load()
	if v == nil || v.(*wrappedValue) == c.lastValue {
		return nil, false
	}
	c.lastValue = v.(*wrappedValue)
	return v.(*wrappedValue).value, true
}

type Pipe struct {
	Sender              Sender
	Receiver            Receiver
	ID                  string
	OnReceiveCompletion func()
	OnSendCompletion    func()
}

type Request struct {
	Payload  interface{}
	Canceled bool
}

type Sender interface {
	Request() Request
	Update(v interface{})
	Finalize(v interface{}, err error)
	Status() Status
	DebugString() string
}

type Receiver interface {
	Receive() bool
	Cancel()
	Status() Status
	Request() interface{}
	DebugString() string
}

type Status struct {
	Canceled  bool
	Completed bool
	Err       error
	Value     interface{}
}

func NewWithFunction(f func(context.Context) (interface{}, error)) (*Pipe, func()) {
	p := New(Request{})
	fmt.Printf("%p) created via NewWithFunction\n", p.Receiver)

	ctx, cancel := context.WithCancel(context.TODO())

	p.OnReceiveCompletion = func() {
		if req := p.Sender.Request(); req.Canceled {
			cancel()
		}
	}

	return p, func() {
		res, err := f(ctx)
		if err != nil {
			p.Sender.Finalize(nil, err)
			return
		}
		p.Sender.Finalize(res, nil)
	}
}

func New(req Request) *Pipe {
	cancelCh := &channel{}
	roundTripCh := &channel{}
	pw := &sender{
		req:         req,
		sendChannel: roundTripCh,
	}
	pr := &receiver{
		req:         req,
		recvChannel: roundTripCh,
		sendChannel: cancelCh,
	}
	fmt.Printf("%p) new receiver with req %+v\n", pr, req)

	p := &Pipe{
		Sender:   pw,
		Receiver: pr,
	}
	id := fmt.Sprintf("%p", p)
	pw.id = id
	pr.id = id
	p.ID = id

	cancelCh.OnSendCompletion = func() {
		v, ok := cancelCh.Receive()
		if ok {
			fmt.Printf("cancelCh.OnSendCompletion %s set %+v\n", id, v.(Request))
			pw.setRequest(v.(Request))
		}
		if p.OnReceiveCompletion != nil {
			fmt.Printf("cancelCh.OnSendCompletion %s calling OnReceiveCompletion\n", id)
			p.OnReceiveCompletion()
		}
	}

	roundTripCh.OnSendCompletion = func() {
		if p.OnSendCompletion != nil {
			p.OnSendCompletion()
		}
	}

	return p
}

type sender struct {
	status      Status
	req         Request
	sendChannel *channel
	mu          sync.Mutex
	id          string
}

func (pw *sender) Status() Status {
	return pw.status
}

func (pw *sender) DebugString() string {
	return pw.id
}

func (pw *sender) Request() Request {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return pw.req
}

func (pw *sender) setRequest(req Request) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.req = req
}

func (pw *sender) Update(v interface{}) {
	fmt.Printf("sender.update id=%s, v=%p\n", pw.id, v)
	pw.status.Value = v
	pw.sendChannel.Send(pw.status)
}

func (pw *sender) Finalize(v interface{}, err error) {
	fmt.Printf("sender.Finalize id=%s, v=%p\n", pw.id, v)
	if v != nil {
		pw.status.Value = v
	}
	pw.status.Err = err
	pw.status.Completed = true
	if errors.Is(err, context.Canceled) && pw.req.Canceled {
		pw.status.Canceled = true
	}
	pw.sendChannel.Send(pw.status)
}

type receiver struct {
	status      Status
	req         Request
	recvChannel *channel
	sendChannel *channel
	id          string
}

func (pr *receiver) DebugString() string {
	return pr.id
}
func (pr *receiver) Request() interface{} {
	//if pr.req.Payload == nil {
	//	// a receiver can have a nil payload when pipe.NewWithFunction(f) is called
	//	panic(fmt.Sprintf("%p: payload is nil when calling Request()", pr))
	//}
	return pr.req.Payload
}

func (pr *receiver) Receive() bool {
	v, ok := pr.recvChannel.Receive()
	if !ok {
		fmt.Printf("receiver.Receive id=%s, not ok\n", pr.id)
		return false
	}
	pr.status = v.(Status)
	fmt.Printf("receiver.Receive id=%s, ok, status set to %+v\n", pr.id, pr.status)
	return true
}

func (pr *receiver) Cancel() {
	fmt.Printf("receiver.Cancel id=%s called by %s\n", pr.id, debug.Stack())
	req := pr.req
	if req.Canceled {
		return
	}
	req.Canceled = true
	pr.sendChannel.Send(req)
}

func (pr *receiver) Status() Status {
	return pr.status
}
