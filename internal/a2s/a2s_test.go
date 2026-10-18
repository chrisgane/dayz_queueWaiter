package a2s

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func infoReply(keywords string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'I', 17})
	for _, s := range []string{"Spaggies Vanilla Nasdara - Hosted by Jack", "nasdara", "dayz", "DayZ"} {
		b.WriteString(s)
		b.WriteByte(0)
	}
	b.Write([]byte{0, 0})              // app id
	b.Write([]byte{70, 70})            // players, max
	b.Write([]byte{0, 'd', 'w', 0, 1}) // bots, type, env, visibility, VAC
	b.WriteString("1.28.160049")
	b.WriteByte(0)
	b.WriteByte(0x80 | 0x10 | 0x20 | 0x01)
	b.Write([]byte{0xFE, 0x08}) // game port 2302
	b.Write(make([]byte, 8))    // Steam ID
	b.WriteString(keywords)
	b.WriteByte(0)
	b.Write(make([]byte, 8)) // game id
	return b.Bytes()
}

// fakeServer answers like a DayZ server: a challenge first, then the info.
func fakeServer(t *testing.T, reply []byte) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	challenge := []byte{1, 2, 3, 4}
	go func() {
		buf := make([]byte, 1400)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if bytes.HasSuffix(buf[:n], challenge) {
				conn.WriteTo(reply, from)
			} else {
				conn.WriteTo(append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'A'}, challenge...), from)
			}
		}
	}()
	return conn.LocalAddr().String()
}

func TestQuery(t *testing.T) {
	addr := fakeServer(t, infoReply("battleye,no3rd,external,privHive,shard,lqs5,etm4.000000,entm2.000000,mod,20:47"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	info, err := Query(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Spaggies Vanilla Nasdara - Hosted by Jack" || info.Players != 70 || info.MaxPlayers != 70 || info.Queue != 5 {
		t.Errorf("got %+v", info)
	}
	if s := info.String(); s != "70/70 players, 5 in queue" {
		t.Errorf("String() = %q", s)
	}
}

func TestQueryNoQueue(t *testing.T) {
	info, err := parseInfo(infoReply("battleye,shard")[5:])
	if err != nil {
		t.Fatal(err)
	}
	if info.Queue != -1 || info.String() != "70/70 players" {
		t.Errorf("got %+v", info)
	}
}

func TestParseShort(t *testing.T) {
	if _, err := parseInfo([]byte{17, 'a', 0}); err == nil {
		t.Error("expected an error")
	}
}

func TestLookupQueryPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("addr") != "54.38.209.2" {
			t.Errorf("addr = %q", r.URL.Query().Get("addr"))
		}
		w.Write([]byte(`{"response":{"success":true,"servers":[
			{"addr":"54.38.209.2:27016","appid":221100,"gamedir":"dayz","gameport":2302},
			{"addr":"54.38.209.2:27116","appid":221100,"gamedir":"dayz","gameport":2402}]}}`))
	}))
	defer srv.Close()
	old := SteamAPI
	SteamAPI = srv.URL + "/"
	defer func() { SteamAPI = old }()

	ctx := context.Background()
	if port, err := LookupQueryPort(ctx, "54.38.209.2", 2402); err != nil || port != 27116 {
		t.Errorf("got %d, %v", port, err)
	}
	if _, err := LookupQueryPort(ctx, "54.38.209.2", 9999); err == nil {
		t.Error("expected an error for an unknown game port")
	}
}
