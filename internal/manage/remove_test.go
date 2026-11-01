package manage

import (
	"testing"

	"neotunnel/internal/config"
)

// A forward's next backup takes over from a removed tunnel, in order.
func TestRemoveKeepsTheOtherBackups(t *testing.T) {
	st := &config.State{Self: "ir", Forwards: []config.Forward{
		{Node: "ir", Tunnel: "a", Backup: "b", MoreBackups: []string{"c", "d"}, Ports: "443", Proto: "tcp"},
		{Node: "ir", Tunnel: "x", Backup: "b", MoreBackups: []string{"c"}, Ports: "80", Proto: "tcp"},
	}}
	removeTunnels(st, []string{"a", "c"})
	if f := st.Forwards[0]; f.Tunnel != "b" || f.Backup != "d" || len(f.MoreBackups) != 0 {
		t.Errorf("443: %+v", f)
	}
	if f := st.Forwards[1]; f.Tunnel != "x" || f.Backup != "b" || len(f.MoreBackups) != 0 {
		t.Errorf("80: %+v", f)
	}
}

func TestRemoveTunnelKeepsForwardOnItsBackup(t *testing.T) {
	st := &config.State{
		Tunnels: []config.Tunnel{{Name: "de1"}, {Name: "de2"}, {Name: "nl"}},
		Forwards: []config.Forward{
			{Node: "ir", Tunnel: "de1", Backup: "de2", Ports: "10400", Proto: "tcp", To: 443},
			{Node: "ir", Tunnel: "de1", Ports: "8080"},
			{Node: "ir", Tunnel: "nl", Backup: "de1", Ports: "2053"},
		},
	}
	removeTunnels(st, []string{"de1"})
	if len(st.Forwards) != 2 {
		t.Fatalf("forwards %+v", st.Forwards)
	}
	if f := st.Forwards[0]; f.Ports != "10400" || f.Tunnel != "de2" || f.Backup != "" || f.To != 443 {
		t.Errorf("10400 should move to its backup de2 with its settings: %+v", f)
	}
	if f := st.Forwards[1]; f.Ports != "2053" || f.Tunnel != "nl" || f.Backup != "" {
		t.Errorf("2053 should stay on nl without a backup: %+v", f)
	}

	// Both of a forward's tunnels gone: the forward goes too.
	removeTunnels(st, []string{"de2"})
	if len(st.Forwards) != 1 || st.Forwards[0].Ports != "2053" {
		t.Errorf("forwards %+v", st.Forwards)
	}
}
