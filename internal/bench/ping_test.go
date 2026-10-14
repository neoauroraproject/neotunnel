package bench

import (
	"testing"
	"time"
)

func TestParsePing(t *testing.T) {
	out := `PING 10.77.0.2 (10.77.0.2) 56(84) bytes of data.

--- 10.77.0.2 ping statistics ---
20 packets transmitted, 19 received, 5% packet loss, time 3805ms
rtt min/avg/max/mdev = 41.120/43.305/52.901/2.114 ms
`
	r, err := ParsePing(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Sent != 20 || r.Received != 19 || r.Loss() != 5 {
		t.Errorf("counts: %+v", r)
	}
	if r.Avg != time.Duration(43.305*float64(time.Millisecond)) || r.Jitter <= 0 {
		t.Errorf("rtt: %+v", r)
	}
	if _, err := ParsePing("3 packets transmitted, 0 received, 100% packet loss"); err == nil {
		t.Error("total loss should be an error")
	}
}
