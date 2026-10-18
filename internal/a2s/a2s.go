// Package a2s asks a DayZ server how full it is, the same way the in-game
// server browser does (Steam's A2S_INFO query).
package a2s

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Info is what the server reports about itself.
type Info struct {
	Name       string
	Map        string
	Players    int
	MaxPlayers int
	// Queue is the number of players waiting to get in, or -1 if the server
	// doesn't say. DayZ reports it as "lqs<n>" in its keywords.
	Queue    int
	Keywords string
}

func (i Info) String() string {
	if i.Queue < 0 {
		return fmt.Sprintf("%d/%d players", i.Players, i.MaxPlayers)
	}
	return fmt.Sprintf("%d/%d players, %d in queue", i.Players, i.MaxPlayers, i.Queue)
}

var infoRequest = append([]byte("\xFF\xFF\xFF\xFFTSource Engine Query"), 0)

// Query asks the server at addr (its query port, not the game port).
func Query(ctx context.Context, addr string) (Info, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return Info{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(3 * time.Second))
	}

	req := infoRequest
	buf := make([]byte, 1400)
	// The server may answer with a challenge first; repeat the request with it.
	for range 3 {
		if _, err := conn.Write(req); err != nil {
			return Info{}, err
		}
		n, err := conn.Read(buf)
		if err != nil {
			return Info{}, err
		}
		p := buf[:n]
		if len(p) < 5 || !bytes.Equal(p[:4], []byte{0xFF, 0xFF, 0xFF, 0xFF}) {
			return Info{}, errors.New("unexpected reply from server")
		}
		switch p[4] {
		case 'A':
			if len(p) < 9 {
				return Info{}, errors.New("short challenge from server")
			}
			req = append(append([]byte{}, infoRequest...), p[5:9]...)
		case 'I':
			return parseInfo(p[5:])
		default:
			return Info{}, fmt.Errorf("unexpected reply type %#x from server", p[4])
		}
	}
	return Info{}, errors.New("server kept sending challenges")
}

func parseInfo(p []byte) (Info, error) {
	r := reader{p: p}
	info := Info{Queue: -1}
	r.byte() // protocol
	info.Name = r.string()
	info.Map = r.string()
	r.string() // folder
	r.string() // game
	r.skip(2)  // app id
	info.Players = int(r.byte())
	info.MaxPlayers = int(r.byte())
	r.skip(5)  // bots, server type, environment, visibility, VAC
	r.string() // version
	if r.err != nil {
		return Info{}, r.err
	}
	edf := r.byte()
	if edf&0x80 != 0 {
		r.skip(2) // game port
	}
	if edf&0x10 != 0 {
		r.skip(8) // Steam ID
	}
	if edf&0x40 != 0 {
		r.skip(2) // spectator port
		r.string()
	}
	if edf&0x20 != 0 {
		info.Keywords = r.string()
	}
	if r.err != nil {
		// Everything that matters came before the extra data.
		return info, nil
	}
	info.Queue = queueSize(info.Keywords)
	return info, nil
}

// queueSize finds "lqs<n>" in DayZ's comma-separated keywords.
func queueSize(keywords string) int {
	for _, k := range strings.Split(keywords, ",") {
		if v, ok := strings.CutPrefix(k, "lqs"); ok {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return -1
}

type reader struct {
	p   []byte
	err error
}

var errShort = errors.New("reply from server cut short")

func (r *reader) byte() byte {
	if len(r.p) < 1 {
		r.err = errShort
		return 0
	}
	b := r.p[0]
	r.p = r.p[1:]
	return b
}

func (r *reader) skip(n int) {
	if len(r.p) < n {
		r.err = errShort
		r.p = nil
		return
	}
	r.p = r.p[n:]
}

func (r *reader) string() string {
	i := bytes.IndexByte(r.p, 0)
	if i < 0 {
		r.err = errShort
		r.p = nil
		return ""
	}
	s := string(r.p[:i])
	r.p = r.p[i+1:]
	return s
}

// SteamAPI is Steam's public server directory.
var SteamAPI = "https://api.steampowered.com/ISteamApps/GetServersAtAddress/v1/"

// LookupQueryPort asks Steam which query port goes with a server's game port
// (the IP:port shown in the server browser).
func LookupQueryPort(ctx context.Context, ip string, gamePort int) (int, error) {
	u := SteamAPI + "?addr=" + url.QueryEscape(ip)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Steam server list: %s", resp.Status)
	}
	var body struct {
		Response struct {
			Success bool
			Servers []struct {
				Addr     string
				GamePort int `json:"gameport"`
			}
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("Steam server list: %w", err)
	}
	for _, s := range body.Response.Servers {
		if s.GamePort != gamePort {
			continue
		}
		_, port, err := net.SplitHostPort(s.Addr)
		if err != nil {
			continue
		}
		if n, err := strconv.Atoi(port); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("Steam doesn't list a server at %s:%d", ip, gamePort)
}
