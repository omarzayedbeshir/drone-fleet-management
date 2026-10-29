package ardupilot

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/bluenviron/gomavlib/v4/pkg/dialect"
	"github.com/bluenviron/gomavlib/v4/pkg/dialects/ardupilotmega"
	"github.com/bluenviron/gomavlib/v4/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v4/pkg/frame"
	"github.com/bluenviron/gomavlib/v4/pkg/message"
	"github.com/bluenviron/gomavlib/v4/pkg/streamwriter"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func TestTCPTransportIntegration(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	acceptedFirst := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		for boot := 1; boot <= 2; boot++ {
			conn, err := listener.Accept()
			if err != nil {
				serverDone <- err
				return
			}
			go io.Copy(io.Discard, conn)
			dialectRW := &dialect.ReadWriter{Dialect: ardupilotmega.Dialect}
			if err := dialectRW.Initialize(); err != nil {
				serverDone <- err
				conn.Close()
				return
			}
			send := func(sys uint8, msg message.Message, corrupt bool) error {
				buffer := bytes.NewBuffer(nil)
				fw := &frame.Writer{ByteWriter: buffer, DialectRW: dialectRW}
				if err := fw.Initialize(); err != nil {
					return err
				}
				sw := &streamwriter.Writer{FrameWriter: fw, Version: streamwriter.V2, SystemID: sys, ComponentID: 1}
				if err := sw.Initialize(); err != nil {
					return err
				}
				if err := sw.Write(msg); err != nil {
					return err
				}
				data := buffer.Bytes()
				if corrupt {
					data[len(data)-1] ^= 0xff
				}
				_, err := conn.Write(data)
				return err
			}
			heartbeat := &common.MessageHeartbeat{Autopilot: common.MAV_AUTOPILOT_ARDUPILOTMEGA, Type: common.MAV_TYPE_QUADROTOR, SystemStatus: common.MAV_STATE_STANDBY}
			if err := send(1, heartbeat, true); err != nil {
				serverDone <- err
				conn.Close()
				return
			}
			if err := send(2, heartbeat, false); err != nil {
				serverDone <- err
				conn.Close()
				return
			}
			for _, msg := range []message.Message{heartbeat, &common.MessageSysStatus{BatteryRemaining: -1},
				&common.MessageParamValue{ParamId: "STAT_BOOTCNT", ParamValue: float32(boot)}, &common.MessageParamValue{ParamId: "STAT_RESET", ParamValue: 1}, &common.MessageParamValue{ParamId: "STAT_FLTTIME", ParamValue: 120}, &common.MessageGpsRawInt{FixType: common.GPS_FIX_TYPE_3D_FIX}, position(1000),
			} {
				if err := send(1, msg, false); err != nil {
					serverDone <- err
					conn.Close()
					return
				}
			}
			if boot == 1 {
				select {
				case <-acceptedFirst:
				case <-ctx.Done():
					conn.Close()
					serverDone <- ctx.Err()
					return
				}
			} else {
				<-ctx.Done()
			}
			conn.Close()
		}
		serverDone <- nil
	}()
	a, err := New(Config{Endpoint: "tcp://" + listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	ids := map[string]bool{}
	if err := a.Run(ctx, func(_ context.Context, sample telemetry.Sample) error {
		count++
		if sample.Source != "ardupilot" || sample.BatteryPercent != nil || sample.Latitude == nil || *sample.Latitude != 30.0444 {
			t.Errorf("invalid filtered observation %+v", sample)
		}
		if ids[sample.EventID] {
			t.Error("reboot/reconnect reused event ID")
		}
		ids[sample.EventID] = true
		if count == 1 {
			close(acceptedFirst)
		} else {
			cancel()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if count != 2 || a.Health().ParseErrors < 2 || a.Health().Accepted != 2 {
		t.Fatalf("transport/filter/reconnect failed: count=%d health=%+v", count, a.Health())
	}
}
