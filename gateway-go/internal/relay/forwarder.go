// Package relay forwards RTP packets from an upstream source to a
// downstream sink without re-encoding. This is the core of the
// gateway's SFU behaviour.
package relay

import (
	"context"
	"errors"
	"fmt"

	"github.com/pion/rtp"
)

// RTPSource abstracts any RTP receiver (*webrtc.TrackRemote in production,
// fakes in tests).
type RTPSource interface {
	ReadRTP() (*rtp.Packet, error)
}

// RTPSink abstracts any RTP sender (*webrtc.TrackLocalStaticRTP in
// production, fakes in tests).
type RTPSink interface {
	WriteRTP(*rtp.Packet) error
}

// Forward pumps packets from src to dst until the source errors out or
// the context is cancelled. Returns the number of packets successfully
// forwarded plus the terminating error (the error is returned even on
// EOF so callers can distinguish graceful shutdown from fatal errors).
func Forward(ctx context.Context, src RTPSource, dst RTPSink) (int, error) {
	if src == nil {
		return 0, fmt.Errorf("relay: nil source")
	}
	if dst == nil {
		return 0, fmt.Errorf("relay: nil sink")
	}
	count := 0
	for {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		default:
		}
		pkt, err := src.ReadRTP()
		if err != nil {
			return count, err
		}
		if err := dst.WriteRTP(pkt); err != nil {
			if errors.Is(err, errClosedPipe) {
				return count, nil
			}
			return count, fmt.Errorf("relay: write: %w", err)
		}
		count++
	}
}

// errClosedPipe sentinel matches io.ErrClosedPipe without importing io
// so tests can stub without runtime dep surface.
var errClosedPipe = errors.New("io: read/write on closed pipe")
