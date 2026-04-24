package relay

import (
	"context"
	"errors"
	"testing"

	"github.com/pion/rtp"
)

type fakeRTPSource struct {
	packets []*rtp.Packet
	idx     int
	err     error
}

func (f *fakeRTPSource) ReadRTP() (*rtp.Packet, error) {
	if f.idx >= len(f.packets) {
		if f.err != nil {
			return nil, f.err
		}
		return nil, errEOF
	}
	p := f.packets[f.idx]
	f.idx++
	return p, nil
}

type fakeRTPSink struct {
	got []*rtp.Packet
}

func (f *fakeRTPSink) WriteRTP(p *rtp.Packet) error {
	f.got = append(f.got, p)
	return nil
}

var errEOF = errors.New("eof")

func TestForward_CopiesUntilSourceEOF(t *testing.T) {
	src := &fakeRTPSource{packets: []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 1}},
		{Header: rtp.Header{SequenceNumber: 2}},
		{Header: rtp.Header{SequenceNumber: 3}},
	}}
	sink := &fakeRTPSink{}
	count, err := Forward(context.Background(), src, sink)
	if !errors.Is(err, errEOF) {
		t.Fatalf("err = %v, want errEOF", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	if len(sink.got) != 3 {
		t.Errorf("sink got %d packets", len(sink.got))
	}
	if sink.got[0].SequenceNumber != 1 || sink.got[2].SequenceNumber != 3 {
		t.Errorf("sequence mismatch: %+v", sink.got)
	}
}

func TestForward_StopsOnContextCancel(t *testing.T) {
	src := &fakeRTPSource{packets: []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 1}},
		{Header: rtp.Header{SequenceNumber: 2}},
	}}
	sink := &fakeRTPSink{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Forward(ctx, src, sink)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestForward_NilSourceError(t *testing.T) {
	sink := &fakeRTPSink{}
	if _, err := Forward(context.Background(), nil, sink); err == nil {
		t.Fatal("expected error for nil source")
	}
}

func TestForward_NilSinkError(t *testing.T) {
	src := &fakeRTPSource{}
	if _, err := Forward(context.Background(), src, nil); err == nil {
		t.Fatal("expected error for nil sink")
	}
}
