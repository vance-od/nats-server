// Copyright 2013-2019 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/nats-io/nats.go"
)

const CLIENT_PORT = -1
const MONITOR_PORT = -1
const CLUSTER_PORT = -1

func init() {
	gwAccountsLimit = 10
}

func DefaultMonitorOptions() *Options {
	return &Options{
		Host:     "127.0.0.1",
		Port:     CLIENT_PORT,
		HTTPHost: "127.0.0.1",
		HTTPPort: MONITOR_PORT,
		NoLog:    true,
		NoSigs:   true,
	}
}

func runMonitorServer() *Server {
	resetPreviousHTTPConnections()
	opts := DefaultMonitorOptions()
	return RunServer(opts)
}

func runMonitorServerNoHTTPPort() *Server {
	resetPreviousHTTPConnections()
	opts := DefaultMonitorOptions()
	opts.HTTPPort = 0
	return RunServer(opts)
}

func resetPreviousHTTPConnections() {
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
}

func TestMyUptime(t *testing.T) {
	// Make sure we print this stuff right.
	var d time.Duration
	var s string

	d = 22 * time.Second
	s = myUptime(d)
	if s != "22s" {
		t.Fatalf("Expected `22s`, go ``%s`", s)
	}
	d = 4*time.Minute + d
	s = myUptime(d)
	if s != "4m22s" {
		t.Fatalf("Expected `4m22s`, go ``%s`", s)
	}
	d = 4*time.Hour + d
	s = myUptime(d)
	if s != "4h4m22s" {
		t.Fatalf("Expected `4h4m22s`, go ``%s`", s)
	}
	d = 32*24*time.Hour + d
	s = myUptime(d)
	if s != "32d4h4m22s" {
		t.Fatalf("Expected `32d4h4m22s`, go ``%s`", s)
	}
	d = 22*365*24*time.Hour + d
	s = myUptime(d)
	if s != "22y32d4h4m22s" {
		t.Fatalf("Expected `22y32d4h4m22s`, go ``%s`", s)
	}
}

// Make sure that we do not run the http server for monitoring unless asked.
func TestNoMonitorPort(t *testing.T) {
	s := runMonitorServerNoHTTPPort()
	defer s.Shutdown()

	// this test might be meaningless now that we're testing with random ports?
	url := fmt.Sprintf("http://127.0.0.1:%d/", 11245)
	if resp, err := http.Get(url + "varz"); err == nil {
		t.Fatalf("Expected error: Got %+v\n", resp)
	}
	if resp, err := http.Get(url + "healthz"); err == nil {
		t.Fatalf("Expected error: Got %+v\n", resp)
	}
	if resp, err := http.Get(url + "connz"); err == nil {
		t.Fatalf("Expected error: Got %+v\n", resp)
	}
}

var (
	appJSONContent = "application/json"
	appJSContent   = "application/javascript"
	textPlain      = "text/plain; charset=utf-8"
)

func readBodyEx(t *testing.T, url string, status int, content string) []byte {
	resp, err := http.Get(url)
	if err != nil {
		stackFatalf(t, "Expected no error: Got %v\n", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		stackFatalf(t, "Expected a %d response, got %d\n", status, resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != content {
		stackFatalf(t, "Expected %s content-type, got %s\n", content, ct)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		stackFatalf(t, "Got an error reading the body: %v\n", err)
	}
	return body
}

func readBody(t *testing.T, url string) []byte {
	return readBodyEx(t, url, http.StatusOK, appJSONContent)
}

func pollVarz(t *testing.T, s *Server, mode int, url string, opts *VarzOptions) *Varz {
	t.Helper()
	if mode == 0 {
		v := &Varz{}
		body := readBody(t, url)
		if err := json.Unmarshal(body, v); err != nil {
			t.Fatalf("Got an error unmarshalling the body: %v\n", err)
		}
		return v
	}
	v, err := s.Varz(opts)
	if err != nil {
		t.Fatalf("Error on Varz: %v", err)
	}
	return v
}

func TestHandleVarz(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)

	for mode := 0; mode < 2; mode++ {
		v := pollVarz(t, s, mode, url+"varz", nil)

		// Do some sanity checks on values
		if time.Since(v.Start) > 10*time.Second {
			t.Fatal("Expected start time to be within 10 seconds.")
		}
	}

	time.Sleep(100 * time.Millisecond)

	nc := createClientConnSubscribeAndPublish(t, s)
	defer nc.Close()

	for mode := 0; mode < 2; mode++ {
		v := pollVarz(t, s, mode, url+"varz", nil)

		if v.Connections != 1 {
			t.Fatalf("Expected Connections of 1, got %v\n", v.Connections)
		}
		if v.TotalConnections < 1 {
			t.Fatalf("Expected Total Connections of at least 1, got %v\n", v.TotalConnections)
		}
		if v.InMsgs != 1 {
			t.Fatalf("Expected InMsgs of 1, got %v\n", v.InMsgs)
		}
		if v.OutMsgs != 1 {
			t.Fatalf("Expected OutMsgs of 1, got %v\n", v.OutMsgs)
		}
		if v.InBytes != 5 {
			t.Fatalf("Expected InBytes of 5, got %v\n", v.InBytes)
		}
		if v.OutBytes != 5 {
			t.Fatalf("Expected OutBytes of 5, got %v\n", v.OutBytes)
		}
		if v.Subscriptions != 0 {
			t.Fatalf("Expected Subscriptions of 0, got %v\n", v.Subscriptions)
		}
	}

	// Test JSONP
	readBodyEx(t, url+"varz?callback=callback", http.StatusOK, appJSContent)
}

func pollConz(t *testing.T, s *Server, mode int, url string, opts *ConnzOptions) *Connz {
	t.Helper()
	if mode == 0 {
		body := readBody(t, url)
		c := &Connz{}
		if err := json.Unmarshal(body, &c); err != nil {
			t.Fatalf("Got an error unmarshalling the body: %v\n", err)
		}
		return c
	}
	c, err := s.Connz(opts)
	if err != nil {
		t.Fatalf("Error on Connz(): %v", err)
	}
	return c
}

func TestConnz(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)

	testConnz := func(mode int) {
		c := pollConz(t, s, mode, url+"connz", nil)

		// Test contents..
		if c.NumConns != 0 {
			t.Fatalf("Expected 0 connections, got %d\n", c.NumConns)
		}
		if c.Total != 0 {
			t.Fatalf("Expected 0 live connections, got %d\n", c.Total)
		}
		if c.Conns == nil || len(c.Conns) != 0 {
			t.Fatalf("Expected 0 connections in array, got %p\n", c.Conns)
		}

		// Test with connections.
		nc := createClientConnSubscribeAndPublish(t, s)
		defer nc.Close()

		time.Sleep(50 * time.Millisecond)

		c = pollConz(t, s, mode, url+"connz", nil)

		if c.NumConns != 1 {
			t.Fatalf("Expected 1 connection, got %d\n", c.NumConns)
		}
		if c.Total != 1 {
			t.Fatalf("Expected 1 live connection, got %d\n", c.Total)
		}
		if c.Conns == nil || len(c.Conns) != 1 {
			t.Fatalf("Expected 1 connection in array, got %d\n", len(c.Conns))
		}

		if c.Limit != DefaultConnListSize {
			t.Fatalf("Expected limit of %d, got %v\n", DefaultConnListSize, c.Limit)
		}

		if c.Offset != 0 {
			t.Fatalf("Expected offset of 0, got %v\n", c.Offset)
		}

		// Test inside details of each connection
		ci := c.Conns[0]

		if ci.Cid == 0 {
			t.Fatalf("Expected non-zero cid, got %v\n", ci.Cid)
		}
		if ci.IP != "127.0.0.1" {
			t.Fatalf("Expected \"127.0.0.1\" for IP, got %v\n", ci.IP)
		}
		if ci.Port == 0 {
			t.Fatalf("Expected non-zero port, got %v\n", ci.Port)
		}
		if ci.NumSubs != 0 {
			t.Fatalf("Expected num_subs of 0, got %v\n", ci.NumSubs)
		}
		if len(ci.Subs) != 0 {
			t.Fatalf("Expected subs of 0, got %v\n", ci.Subs)
		}
		if ci.InMsgs != 1 {
			t.Fatalf("Expected InMsgs of 1, got %v\n", ci.InMsgs)
		}
		if ci.OutMsgs != 1 {
			t.Fatalf("Expected OutMsgs of 1, got %v\n", ci.OutMsgs)
		}
		if ci.InBytes != 5 {
			t.Fatalf("Expected InBytes of 1, got %v\n", ci.InBytes)
		}
		if ci.OutBytes != 5 {
			t.Fatalf("Expected OutBytes of 1, got %v\n", ci.OutBytes)
		}
		if ci.Start.IsZero() {
			t.Fatal("Expected Start to be valid\n")
		}
		if ci.Uptime == "" {
			t.Fatal("Expected Uptime to be valid\n")
		}
		if ci.LastActivity.IsZero() {
			t.Fatal("Expected LastActivity to be valid\n")
		}
		if ci.LastActivity.UnixNano() < ci.Start.UnixNano() {
			t.Fatalf("Expected LastActivity [%v] to be > Start [%v]\n", ci.LastActivity, ci.Start)
		}
		if ci.Idle == "" {
			t.Fatal("Expected Idle to be valid\n")
		}
		if ci.RTT != "" {
			t.Fatal("Expected RTT to NOT be set for new connection\n")
		}
	}

	for mode := 0; mode < 2; mode++ {
		testConnz(mode)
		checkClientsCount(t, s, 0)
	}

	// Test JSONP
	readBodyEx(t, url+"connz?callback=callback", http.StatusOK, appJSContent)
}

func TestConnzBadParams(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/connz?", s.MonitorAddr().Port)
	readBodyEx(t, url+"auth=xxx", http.StatusBadRequest, textPlain)
	readBodyEx(t, url+"subs=xxx", http.StatusBadRequest, textPlain)
	readBodyEx(t, url+"offset=xxx", http.StatusBadRequest, textPlain)
	readBodyEx(t, url+"limit=xxx", http.StatusBadRequest, textPlain)
	readBodyEx(t, url+"state=xxx", http.StatusBadRequest, textPlain)
}

func TestConnzWithSubs(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	nc := createClientConnSubscribeAndPublish(t, s)
	defer nc.Close()

	nc.Subscribe("hello.foo", func(m *nats.Msg) {})
	ensureServerActivityRecorded(t, nc)

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?subs=1", &ConnzOptions{Subscriptions: true})
		// Test inside details of each connection
		ci := c.Conns[0]
		if len(ci.Subs) != 1 || ci.Subs[0] != "hello.foo" {
			t.Fatalf("Expected subs of 1, got %v\n", ci.Subs)
		}
	}
}

func TestConnzWithCID(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	// The one we will request
	cid := 5
	total := 10

	// Create 10
	for i := 1; i <= total; i++ {
		nc := createClientConnSubscribeAndPublish(t, s)
		defer nc.Close()
		if i == cid {
			nc.Subscribe("hello.foo", func(m *nats.Msg) {})
			nc.Subscribe("hello.bar", func(m *nats.Msg) {})
			ensureServerActivityRecorded(t, nc)
		}
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/connz?cid=%d", s.MonitorAddr().Port, cid)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url, &ConnzOptions{CID: uint64(cid)})
		// Test inside details of each connection
		if len(c.Conns) != 1 {
			t.Fatalf("Expected only one connection, but got %d\n", len(c.Conns))
		}
		if c.NumConns != 1 {
			t.Fatalf("Expected NumConns to be 1, but got %d\n", c.NumConns)
		}
		ci := c.Conns[0]
		if ci.Cid != uint64(cid) {
			t.Fatalf("Expected to receive connection %v, but received %v\n", cid, ci.Cid)
		}
		if ci.NumSubs != 2 {
			t.Fatalf("Expected to receive connection with %d subs, but received %d\n", 2, ci.NumSubs)
		}
		// Now test a miss
		badUrl := fmt.Sprintf("http://127.0.0.1:%d/connz?cid=%d", s.MonitorAddr().Port, 100)
		c = pollConz(t, s, mode, badUrl, &ConnzOptions{CID: uint64(100)})
		if len(c.Conns) != 0 {
			t.Fatalf("Expected no connections, got %d\n", len(c.Conns))
		}
		if c.NumConns != 0 {
			t.Fatalf("Expected NumConns of 0, got %d\n", c.NumConns)
		}
	}
}

// Helper to map to connection name
func createConnMap(t *testing.T, cz *Connz) map[string]*ConnInfo {
	cm := make(map[string]*ConnInfo)
	for _, c := range cz.Conns {
		cm[c.Name] = c
	}
	return cm
}

func getFooAndBar(t *testing.T, cm map[string]*ConnInfo) (*ConnInfo, *ConnInfo) {
	return cm["foo"], cm["bar"]
}

func ensureServerActivityRecorded(t *testing.T, nc *nats.Conn) {
	nc.Flush()
	err := nc.Flush()
	if err != nil {
		t.Fatalf("Error flushing: %v\n", err)
	}
}

func TestConnzRTT(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)

	testRTT := func(mode int) {
		// Test with connections.
		nc := createClientConnSubscribeAndPublish(t, s)
		defer nc.Close()

		c := pollConz(t, s, mode, url+"connz", nil)

		if c.NumConns != 1 {
			t.Fatalf("Expected 1 connection, got %d\n", c.NumConns)
		}

		// Send a server side PING to record RTT
		s.mu.Lock()
		ci := c.Conns[0]
		sc := s.clients[ci.Cid]
		if sc == nil {
			t.Fatalf("Error looking up client %v\n", ci.Cid)
		}
		s.mu.Unlock()
		sc.mu.Lock()
		sc.sendPing()
		sc.mu.Unlock()

		// Wait for client to respond with PONG
		time.Sleep(20 * time.Millisecond)

		// Repoll for updated information.
		c = pollConz(t, s, mode, url+"connz", nil)
		ci = c.Conns[0]

		rtt, err := time.ParseDuration(ci.RTT)
		if err != nil {
			t.Fatalf("Could not parse RTT properly, %v (ci.RTT=%v)", err, ci.RTT)
		}
		if rtt <= 0 {
			t.Fatal("Expected RTT to be valid and non-zero\n")
		}
		if rtt > 20*time.Millisecond || rtt < 100*time.Nanosecond {
			t.Fatalf("Invalid RTT of %s\n", ci.RTT)
		}
	}

	for mode := 0; mode < 2; mode++ {
		testRTT(mode)
		checkClientsCount(t, s, 0)
	}
}

func TestConnzLastActivity(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	url += "connz?subs=1"
	opts := &ConnzOptions{Subscriptions: true}

	testActivity := func(mode int) {
		ncFoo := createClientConnWithName(t, "foo", s)
		defer ncFoo.Close()

		ncBar := createClientConnWithName(t, "bar", s)
		defer ncBar.Close()

		// Test inside details of each connection
		ciFoo, ciBar := getFooAndBar(t, createConnMap(t, pollConz(t, s, mode, url, opts)))

		// Test that LastActivity is non-zero
		if ciFoo.LastActivity.IsZero() {
			t.Fatalf("Expected LastActivity for connection '%s'to be valid\n", ciFoo.Name)
		}
		if ciBar.LastActivity.IsZero() {
			t.Fatalf("Expected LastActivity for connection '%s'to be valid\n", ciBar.Name)
		}
		// Foo should be older than Bar
		if ciFoo.LastActivity.After(ciBar.LastActivity) {
			t.Fatal("Expected connection 'foo' to be older than 'bar'\n")
		}

		fooLA := ciFoo.LastActivity
		barLA := ciBar.LastActivity

		ensureServerActivityRecorded(t, ncFoo)
		ensureServerActivityRecorded(t, ncBar)

		// Sub should trigger update.
		sub, _ := ncFoo.Subscribe("hello.world", func(m *nats.Msg) {})
		ensureServerActivityRecorded(t, ncFoo)

		ciFoo, _ = getFooAndBar(t, createConnMap(t, pollConz(t, s, mode, url, opts)))
		nextLA := ciFoo.LastActivity
		if fooLA.Equal(nextLA) {
			t.Fatalf("Subscribe should have triggered update to LastActivity %+v\n", ciFoo)
		}
		fooLA = nextLA

		// Publish and Message Delivery should trigger as well. So both connections
		// should have updates.
		ncBar.Publish("hello.world", []byte("Hello"))

		ensureServerActivityRecorded(t, ncFoo)
		ensureServerActivityRecorded(t, ncBar)

		ciFoo, ciBar = getFooAndBar(t, createConnMap(t, pollConz(t, s, mode, url, opts)))
		nextLA = ciBar.LastActivity
		if barLA.Equal(nextLA) {
			t.Fatalf("Publish should have triggered update to LastActivity\n")
		}
		barLA = nextLA

		// Message delivery on ncFoo should have triggered as well.
		nextLA = ciFoo.LastActivity
		if fooLA.Equal(nextLA) {
			t.Fatalf("Message delivery should have triggered update to LastActivity\n")
		}
		fooLA = nextLA

		// Unsub should trigger as well
		sub.Unsubscribe()
		ensureServerActivityRecorded(t, ncFoo)

		ciFoo, _ = getFooAndBar(t, createConnMap(t, pollConz(t, s, mode, url, opts)))
		nextLA = ciFoo.LastActivity
		if fooLA.Equal(nextLA) {
			t.Fatalf("Message delivery should have triggered update to LastActivity\n")
		}
	}

	for mode := 0; mode < 2; mode++ {
		testActivity(mode)
	}
}

func TestConnzWithOffsetAndLimit(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)

	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?offset=1&limit=1", &ConnzOptions{Offset: 1, Limit: 1})
		if c.Conns == nil || len(c.Conns) != 0 {
			t.Fatalf("Expected 0 connections in array, got %p\n", c.Conns)
		}

		// Test that when given negative values, 0 or default is used
		c = pollConz(t, s, mode, url+"connz?offset=-1&limit=-1", &ConnzOptions{Offset: -11, Limit: -11})
		if c.Conns == nil || len(c.Conns) != 0 {
			t.Fatalf("Expected 0 connections in array, got %p\n", c.Conns)
		}
		if c.Offset != 0 {
			t.Fatalf("Expected offset to be 0, and limit to be %v, got %v and %v",
				DefaultConnListSize, c.Offset, c.Limit)
		}
	}

	cl1 := createClientConnSubscribeAndPublish(t, s)
	defer cl1.Close()

	cl2 := createClientConnSubscribeAndPublish(t, s)
	defer cl2.Close()

	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?offset=1&limit=1", &ConnzOptions{Offset: 1, Limit: 1})
		if c.Limit != 1 {
			t.Fatalf("Expected limit of 1, got %v\n", c.Limit)
		}

		if c.Offset != 1 {
			t.Fatalf("Expected offset of 1, got %v\n", c.Offset)
		}

		if len(c.Conns) != 1 {
			t.Fatalf("Expected conns of 1, got %v\n", len(c.Conns))
		}

		if c.NumConns != 1 {
			t.Fatalf("Expected NumConns to be 1, got %v\n", c.NumConns)
		}

		if c.Total != 2 {
			t.Fatalf("Expected Total to be at least 2, got %v", c.Total)
		}

		c = pollConz(t, s, mode, url+"connz?offset=2&limit=1", &ConnzOptions{Offset: 2, Limit: 1})
		if c.Limit != 1 {
			t.Fatalf("Expected limit of 1, got %v\n", c.Limit)
		}

		if c.Offset != 2 {
			t.Fatalf("Expected offset of 2, got %v\n", c.Offset)
		}

		if len(c.Conns) != 0 {
			t.Fatalf("Expected conns of 0, got %v\n", len(c.Conns))
		}

		if c.NumConns != 0 {
			t.Fatalf("Expected NumConns to be 0, got %v\n", c.NumConns)
		}

		if c.Total != 2 {
			t.Fatalf("Expected Total to be 2, got %v", c.Total)
		}
	}
}

func TestConnzDefaultSorted(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	clients := make([]*nats.Conn, 4)
	for i := range clients {
		clients[i] = createClientConnSubscribeAndPublish(t, s)
		defer clients[i].Close()
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz", nil)
		if c.Conns[0].Cid > c.Conns[1].Cid ||
			c.Conns[1].Cid > c.Conns[2].Cid ||
			c.Conns[2].Cid > c.Conns[3].Cid {
			t.Fatalf("Expected conns sorted in ascending order by cid, got %v < %v\n", c.Conns[0].Cid, c.Conns[3].Cid)
		}
	}
}

func TestConnzSortedByCid(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	clients := make([]*nats.Conn, 4)
	for i := range clients {
		clients[i] = createClientConnSubscribeAndPublish(t, s)
		defer clients[i].Close()
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?sort=cid", &ConnzOptions{Sort: ByCid})
		if c.Conns[0].Cid > c.Conns[1].Cid ||
			c.Conns[1].Cid > c.Conns[2].Cid ||
			c.Conns[2].Cid > c.Conns[3].Cid {
			t.Fatalf("Expected conns sorted in ascending order by cid, got [%v, %v, %v, %v]\n",
				c.Conns[0].Cid, c.Conns[1].Cid, c.Conns[2].Cid, c.Conns[3].Cid)
		}
	}
}

func TestConnzSortedByStart(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	clients := make([]*nats.Conn, 4)
	for i := range clients {
		clients[i] = createClientConnSubscribeAndPublish(t, s)
		defer clients[i].Close()
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?sort=start", &ConnzOptions{Sort: ByStart})
		if c.Conns[0].Start.After(c.Conns[1].Start) ||
			c.Conns[1].Start.After(c.Conns[2].Start) ||
			c.Conns[2].Start.After(c.Conns[3].Start) {
			t.Fatalf("Expected conns sorted in ascending order by startime, got [%v, %v, %v, %v]\n",
				c.Conns[0].Start, c.Conns[1].Start, c.Conns[2].Start, c.Conns[3].Start)
		}
	}
}

func TestConnzSortedByBytesAndMsgs(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	// Create a connection and make it send more messages than others
	firstClient := createClientConnSubscribeAndPublish(t, s)
	for i := 0; i < 100; i++ {
		firstClient.Publish("foo", []byte("Hello World"))
	}
	defer firstClient.Close()
	firstClient.Flush()

	clients := make([]*nats.Conn, 3)
	for i := range clients {
		clients[i] = createClientConnSubscribeAndPublish(t, s)
		defer clients[i].Close()
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?sort=bytes_to", &ConnzOptions{Sort: ByOutBytes})
		if c.Conns[0].OutBytes < c.Conns[1].OutBytes ||
			c.Conns[0].OutBytes < c.Conns[2].OutBytes ||
			c.Conns[0].OutBytes < c.Conns[3].OutBytes {
			t.Fatalf("Expected conns sorted in descending order by bytes to, got %v < one of [%v, %v, %v]\n",
				c.Conns[0].OutBytes, c.Conns[1].OutBytes, c.Conns[2].OutBytes, c.Conns[3].OutBytes)
		}

		c = pollConz(t, s, mode, url+"connz?sort=msgs_to", &ConnzOptions{Sort: ByOutMsgs})
		if c.Conns[0].OutMsgs < c.Conns[1].OutMsgs ||
			c.Conns[0].OutMsgs < c.Conns[2].OutMsgs ||
			c.Conns[0].OutMsgs < c.Conns[3].OutMsgs {
			t.Fatalf("Expected conns sorted in descending order by msgs from, got %v < one of [%v, %v, %v]\n",
				c.Conns[0].OutMsgs, c.Conns[1].OutMsgs, c.Conns[2].OutMsgs, c.Conns[3].OutMsgs)
		}

		c = pollConz(t, s, mode, url+"connz?sort=bytes_from", &ConnzOptions{Sort: ByInBytes})
		if c.Conns[0].InBytes < c.Conns[1].InBytes ||
			c.Conns[0].InBytes < c.Conns[2].InBytes ||
			c.Conns[0].InBytes < c.Conns[3].InBytes {
			t.Fatalf("Expected conns sorted in descending order by bytes from, got %v < one of [%v, %v, %v]\n",
				c.Conns[0].InBytes, c.Conns[1].InBytes, c.Conns[2].InBytes, c.Conns[3].InBytes)
		}

		c = pollConz(t, s, mode, url+"connz?sort=msgs_from", &ConnzOptions{Sort: ByInMsgs})
		if c.Conns[0].InMsgs < c.Conns[1].InMsgs ||
			c.Conns[0].InMsgs < c.Conns[2].InMsgs ||
			c.Conns[0].InMsgs < c.Conns[3].InMsgs {
			t.Fatalf("Expected conns sorted in descending order by msgs from, got %v < one of [%v, %v, %v]\n",
				c.Conns[0].InMsgs, c.Conns[1].InMsgs, c.Conns[2].InMsgs, c.Conns[3].InMsgs)
		}
	}
}

func TestConnzSortedByPending(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	firstClient := createClientConnSubscribeAndPublish(t, s)
	firstClient.Subscribe("hello.world", func(m *nats.Msg) {})
	clients := make([]*nats.Conn, 3)
	for i := range clients {
		clients[i] = createClientConnSubscribeAndPublish(t, s)
		defer clients[i].Close()
	}
	defer firstClient.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?sort=pending", &ConnzOptions{Sort: ByPending})
		if c.Conns[0].Pending < c.Conns[1].Pending ||
			c.Conns[0].Pending < c.Conns[2].Pending ||
			c.Conns[0].Pending < c.Conns[3].Pending {
			t.Fatalf("Expected conns sorted in descending order by number of pending, got %v < one of [%v, %v, %v]\n",
				c.Conns[0].Pending, c.Conns[1].Pending, c.Conns[2].Pending, c.Conns[3].Pending)
		}
	}
}

func TestConnzSortedBySubs(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	firstClient := createClientConnSubscribeAndPublish(t, s)
	firstClient.Subscribe("hello.world", func(m *nats.Msg) {})
	defer firstClient.Close()

	clients := make([]*nats.Conn, 3)
	for i := range clients {
		clients[i] = createClientConnSubscribeAndPublish(t, s)
		defer clients[i].Close()
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?sort=subs", &ConnzOptions{Sort: BySubs})
		if c.Conns[0].NumSubs < c.Conns[1].NumSubs ||
			c.Conns[0].NumSubs < c.Conns[2].NumSubs ||
			c.Conns[0].NumSubs < c.Conns[3].NumSubs {
			t.Fatalf("Expected conns sorted in descending order by number of subs, got %v < one of [%v, %v, %v]\n",
				c.Conns[0].NumSubs, c.Conns[1].NumSubs, c.Conns[2].NumSubs, c.Conns[3].NumSubs)
		}
	}
}

func TestConnzSortedByLast(t *testing.T) {
	resetPreviousHTTPConnections()
	opts := DefaultMonitorOptions()
	s := RunServer(opts)
	defer s.Shutdown()

	firstClient := createClientConnSubscribeAndPublish(t, s)
	defer firstClient.Close()
	firstClient.Subscribe("hello.world", func(m *nats.Msg) {})
	firstClient.Flush()

	clients := make([]*nats.Conn, 3)
	for i := range clients {
		clients[i] = createClientConnSubscribeAndPublish(t, s)
		defer clients[i].Close()
		clients[i].Flush()
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?sort=last", &ConnzOptions{Sort: ByLast})
		if c.Conns[0].LastActivity.UnixNano() < c.Conns[1].LastActivity.UnixNano() ||
			c.Conns[1].LastActivity.UnixNano() < c.Conns[2].LastActivity.UnixNano() ||
			c.Conns[2].LastActivity.UnixNano() < c.Conns[3].LastActivity.UnixNano() {
			t.Fatalf("Expected conns sorted in descending order by lastActivity, got %v < one of [%v, %v, %v]\n",
				c.Conns[0].LastActivity, c.Conns[1].LastActivity, c.Conns[2].LastActivity, c.Conns[3].LastActivity)
		}
	}
}

func TestConnzSortedByUptime(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	for i := 0; i < 4; i++ {
		client := createClientConnSubscribeAndPublish(t, s)
		defer client.Close()
		// Since we check times (now-start) does not have to be big.
		time.Sleep(50 * time.Millisecond)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?sort=uptime", &ConnzOptions{Sort: ByUptime})
		now := time.Now()
		ups := make([]int, 4)
		for i := 0; i < 4; i++ {
			ups[i] = int(now.Sub(c.Conns[i].Start))
		}
		if !sort.IntsAreSorted(ups) {
			d := make([]time.Duration, 4)
			for i := 0; i < 4; i++ {
				d[i] = time.Duration(ups[i])
			}
			t.Fatalf("Expected conns sorted in ascending order by uptime (now-Start), got %+v\n", d)
		}
	}
}

func TestConnzSortedByUptimeClosedConn(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	for i := time.Duration(1); i <= 4; i++ {
		c := createClientConnSubscribeAndPublish(t, s)

		// Grab client and asjust start time such that
		client := s.getClient(uint64(i))
		if client == nil {
			t.Fatalf("Could nopt retrieve client for %d\n", i)
		}
		client.mu.Lock()
		client.start = client.start.Add(-10 * (4 - i) * time.Second)
		client.mu.Unlock()

		c.Close()
	}

	checkClosedConns(t, s, 4, time.Second)

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?state=closed&sort=uptime", &ConnzOptions{State: ConnClosed, Sort: ByUptime})
		ups := make([]int, 4)
		for i := 0; i < 4; i++ {
			ups[i] = int(c.Conns[i].Stop.Sub(c.Conns[i].Start))
		}
		if !sort.IntsAreSorted(ups) {
			d := make([]time.Duration, 4)
			for i := 0; i < 4; i++ {
				d[i] = time.Duration(ups[i])
			}
			t.Fatalf("Expected conns sorted in ascending order by uptime, got %+v\n", d)
		}
	}
}

func TestConnzSortedByStopOnOpen(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	opts := s.getOpts()
	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)

	// 4 clients
	for i := 0; i < 4; i++ {
		c, err := nats.Connect(url)
		if err != nil {
			t.Fatalf("Could not create client: %v\n", err)
		}
		defer c.Close()
	}

	c, err := s.Connz(&ConnzOptions{Sort: ByStop})
	if err == nil {
		t.Fatalf("Expected err to be non-nil, got %+v\n", c)
	}
}

func TestConnzSortedByStopTimeClosedConn(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	opts := s.getOpts()
	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)

	// 4 clients
	for i := 0; i < 4; i++ {
		c, err := nats.Connect(url)
		if err != nil {
			t.Fatalf("Could not create client: %v\n", err)
		}
		c.Close()
	}
	checkClosedConns(t, s, 4, time.Second)

	//Now adjust the Stop times for these with some random values.
	s.mu.Lock()
	now := time.Now()
	ccs := s.closed.closedClients()
	for _, cc := range ccs {
		newStop := now.Add(time.Duration(rand.Int()%120) * -time.Minute)
		cc.Stop = &newStop
	}
	s.mu.Unlock()

	url = fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?state=closed&sort=stop", &ConnzOptions{State: ConnClosed, Sort: ByStop})
		ups := make([]int, 4)
		nowU := time.Now().UnixNano()
		for i := 0; i < 4; i++ {
			ups[i] = int(nowU - c.Conns[i].Stop.UnixNano())
		}
		if !sort.IntsAreSorted(ups) {
			d := make([]time.Duration, 4)
			for i := 0; i < 4; i++ {
				d[i] = time.Duration(ups[i])
			}
			t.Fatalf("Expected conns sorted in ascending order by stop time, got %+v\n", d)
		}
	}
}

func TestConnzSortedByReason(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	opts := s.getOpts()
	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)

	// 20 clients
	for i := 0; i < 20; i++ {
		c, err := nats.Connect(url)
		if err != nil {
			t.Fatalf("Could not create client: %v\n", err)
		}
		c.Close()
	}
	checkClosedConns(t, s, 20, time.Second)

	//Now adjust the Reasons for these with some random values.
	s.mu.Lock()
	ccs := s.closed.closedClients()
	max := int(ServerShutdown)
	for _, cc := range ccs {
		cc.Reason = ClosedState(rand.Int() % max).String()
	}
	s.mu.Unlock()

	url = fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz?state=closed&sort=reason", &ConnzOptions{State: ConnClosed, Sort: ByReason})
		rs := make([]string, 20)
		for i := 0; i < 20; i++ {
			rs[i] = c.Conns[i].Reason
		}
		if !sort.StringsAreSorted(rs) {
			t.Fatalf("Expected conns sorted in order by stop reason, got %#v\n", rs)
		}
	}
}

func TestConnzSortedByReasonOnOpen(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	opts := s.getOpts()
	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)

	// 4 clients
	for i := 0; i < 4; i++ {
		c, err := nats.Connect(url)
		if err != nil {
			t.Fatalf("Could not create client: %v\n", err)
		}
		defer c.Close()
	}

	c, err := s.Connz(&ConnzOptions{Sort: ByReason})
	if err == nil {
		t.Fatalf("Expected err to be non-nil, got %+v\n", c)
	}
}

func TestConnzSortedByIdle(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)

	testIdle := func(mode int) {
		firstClient := createClientConnSubscribeAndPublish(t, s)
		defer firstClient.Close()
		firstClient.Subscribe("client.1", func(m *nats.Msg) {})
		firstClient.Flush()

		secondClient := createClientConnSubscribeAndPublish(t, s)
		defer secondClient.Close()

		// Make it such that the second client started 10 secs ago. 10 is important since bug
		// was strcmp, e.g. 1s vs 11s
		var cid uint64
		switch mode {
		case 0:
			cid = uint64(2)
		case 1:
			cid = uint64(4)
		}
		client := s.getClient(cid)
		if client == nil {
			t.Fatalf("Error looking up client %v\n", 2)
		}

		// We want to make sure that we set start/last after the server has finished
		// updating this client's last activity. Doing another Flush() now (even though
		// one is done in createClientConnSubscribeAndPublish) ensures that server has
		// finished updating the client's last activity, since for that last flush there
		// should be no new message/sub/unsub activity.
		secondClient.Flush()

		client.mu.Lock()
		client.start = client.start.Add(-10 * time.Second)
		client.last = client.start
		client.mu.Unlock()

		// The Idle granularity is a whole second
		time.Sleep(time.Second)
		firstClient.Publish("client.1", []byte("new message"))

		c := pollConz(t, s, mode, url+"connz?sort=idle", &ConnzOptions{Sort: ByIdle})
		// Make sure we are returned 2 connections...
		if len(c.Conns) != 2 {
			t.Fatalf("Expected to get two connections, got %v", len(c.Conns))
		}

		// And that the Idle time is valid (even if equal to "0s")
		if c.Conns[0].Idle == "" || c.Conns[1].Idle == "" {
			t.Fatal("Expected Idle value to be valid")
		}

		idle1, err := time.ParseDuration(c.Conns[0].Idle)
		if err != nil {
			t.Fatalf("Unable to parse duration %v, err=%v", c.Conns[0].Idle, err)
		}
		idle2, err := time.ParseDuration(c.Conns[1].Idle)
		if err != nil {
			t.Fatalf("Unable to parse duration %v, err=%v", c.Conns[0].Idle, err)
		}

		if idle2 < idle1 {
			t.Fatalf("Expected conns sorted in descending order by Idle, got %v < %v\n",
				idle2, idle1)
		}
	}
	for mode := 0; mode < 2; mode++ {
		testIdle(mode)
	}
}

func TestConnzSortBadRequest(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	firstClient := createClientConnSubscribeAndPublish(t, s)
	firstClient.Subscribe("hello.world", func(m *nats.Msg) {})
	clients := make([]*nats.Conn, 3)
	for i := range clients {
		clients[i] = createClientConnSubscribeAndPublish(t, s)
		defer clients[i].Close()
	}
	defer firstClient.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	readBodyEx(t, url+"connz?sort=foo", http.StatusBadRequest, textPlain)

	if _, err := s.Connz(&ConnzOptions{Sort: "foo"}); err == nil {
		t.Fatal("Expected error, got none")
	}
}

func pollRoutez(t *testing.T, s *Server, mode int, url string, opts *RoutezOptions) *Routez {
	t.Helper()
	if mode == 0 {
		rz := &Routez{}
		body := readBody(t, url)
		if err := json.Unmarshal(body, rz); err != nil {
			t.Fatalf("Got an error unmarshalling the body: %v\n", err)
		}
		return rz
	}
	rz, err := s.Routez(opts)
	if err != nil {
		t.Fatalf("Error on Routez: %v", err)
	}
	return rz
}

func TestConnzWithRoutes(t *testing.T) {
	resetPreviousHTTPConnections()
	opts := DefaultMonitorOptions()
	opts.Cluster.Host = "127.0.0.1"
	opts.Cluster.Port = CLUSTER_PORT

	s := RunServer(opts)
	defer s.Shutdown()

	opts = &Options{
		Host: "127.0.0.1",
		Port: -1,
		Cluster: ClusterOpts{
			Host: "127.0.0.1",
			Port: -1,
		},
		NoLog:  true,
		NoSigs: true,
	}
	routeURL, _ := url.Parse(fmt.Sprintf("nats-route://127.0.0.1:%d", s.ClusterAddr().Port))
	opts.Routes = []*url.URL{routeURL}

	sc := RunServer(opts)
	defer sc.Shutdown()

	checkClusterFormed(t, s, sc)

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		c := pollConz(t, s, mode, url+"connz", nil)
		// Test contents..
		// Make sure routes don't show up under connz, but do under routez
		if c.NumConns != 0 {
			t.Fatalf("Expected 0 connections, got %d\n", c.NumConns)
		}
		if c.Conns == nil || len(c.Conns) != 0 {
			t.Fatalf("Expected 0 connections in array, got %p\n", c.Conns)
		}
	}

	nc := createClientConnSubscribeAndPublish(t, sc)
	defer nc.Close()

	nc.Subscribe("hello.bar", func(m *nats.Msg) {})
	nc.Flush()
	checkExpectedSubs(t, 1, s, sc)

	// Now check routez
	urls := []string{"routez", "routez?subs=1"}
	for subs, urlSuffix := range urls {
		for mode := 0; mode < 2; mode++ {
			rz := pollRoutez(t, s, mode, url+urlSuffix, &RoutezOptions{Subscriptions: subs == 1})

			if rz.NumRoutes != 1 {
				t.Fatalf("Expected 1 route, got %d\n", rz.NumRoutes)
			}

			if len(rz.Routes) != 1 {
				t.Fatalf("Expected route array of 1, got %v\n", len(rz.Routes))
			}

			route := rz.Routes[0]

			if route.DidSolicit {
				t.Fatalf("Expected unsolicited route, got %v\n", route.DidSolicit)
			}

			// Don't ask for subs, so there should not be any
			if subs == 0 {
				if len(route.Subs) != 0 {
					t.Fatalf("There should not be subs, got %v", len(route.Subs))
				}
			} else {
				if len(route.Subs) != 1 {
					t.Fatalf("There should be 1 sub, got %v", len(route.Subs))
				}
			}
		}
	}

	// Test JSONP
	readBodyEx(t, url+"routez?callback=callback", http.StatusOK, appJSContent)
}

func TestRoutezWithBadParams(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/routez?", s.MonitorAddr().Port)
	readBodyEx(t, url+"subs=xxx", http.StatusBadRequest, textPlain)
}

func pollSubsz(t *testing.T, s *Server, mode int, url string, opts *SubszOptions) *Subsz {
	t.Helper()
	if mode == 0 {
		body := readBody(t, url)
		sz := &Subsz{}
		if err := json.Unmarshal(body, sz); err != nil {
			t.Fatalf("Got an error unmarshalling the body: %v\n", err)
		}
		return sz
	}
	sz, err := s.Subsz(opts)
	if err != nil {
		t.Fatalf("Error on Subsz: %v", err)
	}
	return sz
}

func TestSubsz(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	nc := createClientConnSubscribeAndPublish(t, s)
	defer nc.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)

	for mode := 0; mode < 2; mode++ {
		sl := pollSubsz(t, s, mode, url+"subsz", nil)
		if sl.NumSubs != 0 {
			t.Fatalf("Expected NumSubs of 0, got %d\n", sl.NumSubs)
		}
		if sl.NumInserts != 1 {
			t.Fatalf("Expected NumInserts of 1, got %d\n", sl.NumInserts)
		}
		if sl.NumMatches != 1 {
			t.Fatalf("Expected NumMatches of 1, got %d\n", sl.NumMatches)
		}
	}

	// Test JSONP
	readBodyEx(t, url+"subsz?callback=callback", http.StatusOK, appJSContent)
}

func TestSubszDetails(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	nc := createClientConnSubscribeAndPublish(t, s)
	defer nc.Close()

	nc.Subscribe("foo.*", func(m *nats.Msg) {})
	nc.Subscribe("foo.bar", func(m *nats.Msg) {})
	nc.Subscribe("foo.foo", func(m *nats.Msg) {})

	nc.Publish("foo.bar", []byte("Hello"))
	nc.Publish("foo.baz", []byte("Hello"))
	nc.Publish("foo.foo", []byte("Hello"))

	nc.Flush()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)

	for mode := 0; mode < 2; mode++ {
		sl := pollSubsz(t, s, mode, url+"subsz?subs=1", &SubszOptions{Subscriptions: true})
		if sl.NumSubs != 3 {
			t.Fatalf("Expected NumSubs of 3, got %d\n", sl.NumSubs)
		}
		if sl.Total != 3 {
			t.Fatalf("Expected Total of 3, got %d\n", sl.Total)
		}
		if len(sl.Subs) != 3 {
			t.Fatalf("Expected subscription details for 3 subs, got %d\n", len(sl.Subs))
		}
	}
}

func TestSubszWithOffsetAndLimit(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	nc := createClientConnSubscribeAndPublish(t, s)
	defer nc.Close()

	for i := 0; i < 200; i++ {
		nc.Subscribe(fmt.Sprintf("foo.%d", i), func(m *nats.Msg) {})
	}
	nc.Flush()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		sl := pollSubsz(t, s, mode, url+"subsz?subs=1&offset=10&limit=100", &SubszOptions{Subscriptions: true, Offset: 10, Limit: 100})
		if sl.NumSubs != 200 {
			t.Fatalf("Expected NumSubs of 200, got %d\n", sl.NumSubs)
		}
		if sl.Total != 100 {
			t.Fatalf("Expected Total of 100, got %d\n", sl.Total)
		}
		if sl.Offset != 10 {
			t.Fatalf("Expected Offset of 10, got %d\n", sl.Offset)
		}
		if sl.Limit != 100 {
			t.Fatalf("Expected Total of 100, got %d\n", sl.Limit)
		}
		if len(sl.Subs) != 100 {
			t.Fatalf("Expected subscription details for 100 subs, got %d\n", len(sl.Subs))
		}
	}
}

func TestSubszTestPubSubject(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	nc := createClientConnSubscribeAndPublish(t, s)
	defer nc.Close()

	nc.Subscribe("foo.*", func(m *nats.Msg) {})
	nc.Subscribe("foo.bar", func(m *nats.Msg) {})
	nc.Subscribe("foo.foo", func(m *nats.Msg) {})
	nc.Flush()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		sl := pollSubsz(t, s, mode, url+"subsz?subs=1&test=foo.foo", &SubszOptions{Subscriptions: true, Test: "foo.foo"})
		if sl.Total != 2 {
			t.Fatalf("Expected Total of 2 match, got %d\n", sl.Total)
		}
		if len(sl.Subs) != 2 {
			t.Fatalf("Expected subscription details for 2 matching subs, got %d\n", len(sl.Subs))
		}
		sl = pollSubsz(t, s, mode, url+"subsz?subs=1&test=foo", &SubszOptions{Subscriptions: true, Test: "foo"})
		if len(sl.Subs) != 0 {
			t.Fatalf("Expected no matching subs, got %d\n", len(sl.Subs))
		}
	}
	// Make sure we get an error with invalid test subject.
	testUrl := url + "subsz?subs=1&"
	readBodyEx(t, testUrl+"test=*", http.StatusBadRequest, textPlain)
	readBodyEx(t, testUrl+"test=foo.*", http.StatusBadRequest, textPlain)
	readBodyEx(t, testUrl+"test=foo.>", http.StatusBadRequest, textPlain)
	readBodyEx(t, testUrl+"test=foo..bar", http.StatusBadRequest, textPlain)
}

// Tests handle root
func TestHandleRoot(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	nc := createClientConnSubscribeAndPublish(t, s)
	defer nc.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port))
	if err != nil {
		t.Fatalf("Expected no error: Got %v\n", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected a %d response, got %d\n", http.StatusOK, resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Expected no error reading body: Got %v\n", err)
	}
	for _, b := range body {
		if b > unicode.MaxASCII {
			t.Fatalf("Expected body to contain only ASCII characters, but got %v\n", b)
		}
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("Expected text/html response, got %s\n", ct)
	}
	defer resp.Body.Close()
}

func TestConnzWithNamedClient(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	clientName := "test-client"
	nc := createClientConnWithName(t, clientName, s)
	defer nc.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		// Confirm server is exposing client name in monitoring endpoint.
		c := pollConz(t, s, mode, url+"connz", nil)
		got := len(c.Conns)
		expected := 1
		if got != expected {
			t.Fatalf("Expected %d connection in array, got %d\n", expected, got)
		}

		conn := c.Conns[0]
		if conn.Name != clientName {
			t.Fatalf("Expected client to have name %q. got %q", clientName, conn.Name)
		}
	}
}

func TestConnzWithStateForClosedConns(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	numEach := 10
	// Create 10 closed, and 10 to leave open.
	for i := 0; i < numEach; i++ {
		nc := createClientConnSubscribeAndPublish(t, s)
		nc.Subscribe("hello.closed.conns", func(m *nats.Msg) {})
		nc.Close()
		nc = createClientConnSubscribeAndPublish(t, s)
		nc.Subscribe("hello.open.conns", func(m *nats.Msg) {})
		defer nc.Close()
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)

	for mode := 0; mode < 2; mode++ {
		checkFor(t, 2*time.Second, 10*time.Millisecond, func() error {
			// Look at all open
			c := pollConz(t, s, mode, url+"connz?state=open", &ConnzOptions{State: ConnOpen})
			if lc := len(c.Conns); lc != numEach {
				return fmt.Errorf("Expected %d connections in array, got %d", numEach, lc)
			}
			// Look at all closed
			c = pollConz(t, s, mode, url+"connz?state=closed", &ConnzOptions{State: ConnClosed})
			if lc := len(c.Conns); lc != numEach {
				return fmt.Errorf("Expected %d connections in array, got %d", numEach, lc)
			}
			// Look at all
			c = pollConz(t, s, mode, url+"connz?state=ALL", &ConnzOptions{State: ConnAll})
			if lc := len(c.Conns); lc != numEach*2 {
				return fmt.Errorf("Expected %d connections in array, got %d", 2*numEach, lc)
			}
			// Look at CID #1, which is in closed.
			c = pollConz(t, s, mode, url+"connz?cid=1&state=open", &ConnzOptions{CID: 1, State: ConnOpen})
			if lc := len(c.Conns); lc != 0 {
				return fmt.Errorf("Expected no connections in open array, got %d", lc)
			}
			c = pollConz(t, s, mode, url+"connz?cid=1&state=closed", &ConnzOptions{CID: 1, State: ConnClosed})
			if lc := len(c.Conns); lc != 1 {
				return fmt.Errorf("Expected a connection in closed array, got %d", lc)
			}
			c = pollConz(t, s, mode, url+"connz?cid=1&state=ALL", &ConnzOptions{CID: 1, State: ConnAll})
			if lc := len(c.Conns); lc != 1 {
				return fmt.Errorf("Expected a connection in closed array, got %d", lc)
			}
			c = pollConz(t, s, mode, url+"connz?cid=1&state=closed&subs=true",
				&ConnzOptions{CID: 1, State: ConnClosed, Subscriptions: true})
			if lc := len(c.Conns); lc != 1 {
				return fmt.Errorf("Expected a connection in closed array, got %d", lc)
			}
			ci := c.Conns[0]
			if ci.NumSubs != 1 {
				return fmt.Errorf("Expected NumSubs to be 1, got %d", ci.NumSubs)
			}
			if len(ci.Subs) != 1 {
				return fmt.Errorf("Expected len(ci.Subs) to be 1 also, got %d", len(ci.Subs))
			}
			// Now ask for same thing without subs and make sure they are not returned.
			c = pollConz(t, s, mode, url+"connz?cid=1&state=closed&subs=false",
				&ConnzOptions{CID: 1, State: ConnClosed, Subscriptions: false})
			if lc := len(c.Conns); lc != 1 {
				return fmt.Errorf("Expected a connection in closed array, got %d", lc)
			}
			ci = c.Conns[0]
			if ci.NumSubs != 1 {
				return fmt.Errorf("Expected NumSubs to be 1, got %d", ci.NumSubs)
			}
			if len(ci.Subs) != 0 {
				return fmt.Errorf("Expected len(ci.Subs) to be 0 since subs=false, got %d", len(ci.Subs))
			}

			// CID #2 is in open
			c = pollConz(t, s, mode, url+"connz?cid=2&state=open", &ConnzOptions{CID: 2, State: ConnOpen})
			if lc := len(c.Conns); lc != 1 {
				return fmt.Errorf("Expected a connection in open array, got %d", lc)
			}
			c = pollConz(t, s, mode, url+"connz?cid=2&state=closed", &ConnzOptions{CID: 2, State: ConnClosed})
			if lc := len(c.Conns); lc != 0 {
				return fmt.Errorf("Expected no connections in closed array, got %d", lc)
			}
			return nil
		})
	}
}

// Make sure options for ConnInfo like subs=1, authuser, etc do not cause a race.
func TestConnzClosedConnsRace(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	// Create 100 closed connections.
	for i := 0; i < 100; i++ {
		nc := createClientConnSubscribeAndPublish(t, s)
		nc.Close()
	}

	urlWithoutSubs := fmt.Sprintf("http://127.0.0.1:%d/connz?state=closed", s.MonitorAddr().Port)
	urlWithSubs := urlWithoutSubs + "&subs=true"

	checkClosedConns(t, s, 100, 2*time.Second)

	wg := &sync.WaitGroup{}

	fn := func(url string) {
		deadline := time.Now().Add(1 * time.Second)
		for time.Now().Before(deadline) {
			c := pollConz(t, s, 0, url, nil)
			if len(c.Conns) != 100 {
				t.Errorf("Incorrect Results: %+v\n", c)
			}
		}
		wg.Done()
	}

	wg.Add(2)
	go fn(urlWithSubs)
	go fn(urlWithoutSubs)
	wg.Wait()
}

// Make sure a bad client that is disconnected right away has proper values.
func TestConnzClosedConnsBadClient(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	opts := s.getOpts()

	rc, err := net.Dial("tcp", fmt.Sprintf("%s:%d", opts.Host, opts.Port))
	if err != nil {
		t.Fatalf("Error on dial: %v", err)
	}
	rc.Close()

	checkClosedConns(t, s, 1, 2*time.Second)

	c := pollConz(t, s, 1, "", &ConnzOptions{State: ConnClosed})
	if len(c.Conns) != 1 {
		t.Errorf("Incorrect Results: %+v\n", c)
	}
	ci := c.Conns[0]

	uptime := ci.Stop.Sub(ci.Start)
	idle, err := time.ParseDuration(ci.Idle)
	if err != nil {
		t.Fatalf("Could not parse Idle: %v\n", err)
	}
	if idle > uptime {
		t.Fatalf("Idle can't be larger then uptime, %v vs %v\n", idle, uptime)
	}
	if ci.LastActivity.IsZero() {
		t.Fatalf("LastActivity should not be Zero\n")
	}
}

// Make sure a bad client that tries to connect plain to TLS has proper values.
func TestConnzClosedConnsBadTLSClient(t *testing.T) {
	resetPreviousHTTPConnections()

	tc := &TLSConfigOpts{}
	tc.CertFile = "configs/certs/server.pem"
	tc.KeyFile = "configs/certs/key.pem"

	var err error
	opts := DefaultMonitorOptions()
	opts.TLSTimeout = 1.5 // 1.5 seconds
	opts.TLSConfig, err = GenTLSConfig(tc)
	if err != nil {
		t.Fatalf("Error creating TSL config: %v", err)
	}

	s := RunServer(opts)
	defer s.Shutdown()

	opts = s.getOpts()

	rc, err := net.Dial("tcp", fmt.Sprintf("%s:%d", opts.Host, opts.Port))
	if err != nil {
		t.Fatalf("Error on dial: %v", err)
	}
	rc.Write([]byte("CONNECT {}\r\n"))
	rc.Close()

	checkClosedConns(t, s, 1, 2*time.Second)

	c := pollConz(t, s, 1, "", &ConnzOptions{State: ConnClosed})
	if len(c.Conns) != 1 {
		t.Errorf("Incorrect Results: %+v\n", c)
	}
	ci := c.Conns[0]

	uptime := ci.Stop.Sub(ci.Start)
	idle, err := time.ParseDuration(ci.Idle)
	if err != nil {
		t.Fatalf("Could not parse Idle: %v\n", err)
	}
	if idle > uptime {
		t.Fatalf("Idle can't be larger then uptime, %v vs %v\n", idle, uptime)
	}
	if ci.LastActivity.IsZero() {
		t.Fatalf("LastActivity should not be Zero\n")
	}
}

// Create a connection to test ConnInfo
func createClientConnSubscribeAndPublish(t *testing.T, s *Server) *nats.Conn {
	natsURL := fmt.Sprintf("nats://127.0.0.1:%d", s.Addr().(*net.TCPAddr).Port)
	client := nats.DefaultOptions
	client.Servers = []string{natsURL}
	nc, err := client.Connect()
	if err != nil {
		t.Fatalf("Error creating client: %v to: %s\n", err, natsURL)
	}

	ch := make(chan bool)
	inbox := nats.NewInbox()
	sub, err := nc.Subscribe(inbox, func(m *nats.Msg) { ch <- true })
	if err != nil {
		t.Fatalf("Error subscribing to `%s`: %v\n", inbox, err)
	}
	nc.Publish(inbox, []byte("Hello"))
	// Wait for message
	<-ch
	sub.Unsubscribe()
	close(ch)
	nc.Flush()
	return nc
}

func createClientConnWithName(t *testing.T, name string, s *Server) *nats.Conn {
	natsURI := fmt.Sprintf("nats://127.0.0.1:%d", s.Addr().(*net.TCPAddr).Port)

	client := nats.DefaultOptions
	client.Servers = []string{natsURI}
	client.Name = name
	nc, err := client.Connect()
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	return nc
}

func TestStacksz(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	body := readBody(t, url+"stacksz")
	// Check content
	str := string(body)
	if !strings.Contains(str, "HandleStacksz") {
		t.Fatalf("Result does not seem to contain server's stacks:\n%v", str)
	}
}

func TestConcurrentMonitoring(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)
	// Get some endpoints. Make sure we have at least varz,
	// and the more the merrier.
	endpoints := []string{"varz", "varz", "varz", "connz", "connz", "subsz", "subsz", "routez", "routez"}
	wg := &sync.WaitGroup{}
	wg.Add(len(endpoints))
	ech := make(chan string, len(endpoints))

	for _, e := range endpoints {
		go func(endpoint string) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				resp, err := http.Get(url + endpoint)
				if err != nil {
					ech <- fmt.Sprintf("Expected no error: Got %v\n", err)
					return
				}
				if resp.StatusCode != http.StatusOK {
					ech <- fmt.Sprintf("Expected a %v response, got %d\n", http.StatusOK, resp.StatusCode)
					return
				}
				ct := resp.Header.Get("Content-Type")
				if ct != "application/json" {
					ech <- fmt.Sprintf("Expected application/json content-type, got %s\n", ct)
					return
				}
				defer resp.Body.Close()
				if _, err := ioutil.ReadAll(resp.Body); err != nil {
					ech <- fmt.Sprintf("Got an error reading the body: %v\n", err)
					return
				}
				resp.Body.Close()
			}
		}(e)
	}
	wg.Wait()
	// Check for any errors
	select {
	case err := <-ech:
		t.Fatal(err)
	default:
	}
}

func TestMonitorHandler(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()
	handler := s.HTTPHandler()
	if handler == nil {
		t.Fatal("HTTP Handler should be set")
	}
	s.Shutdown()
	handler = s.HTTPHandler()
	if handler != nil {
		t.Fatal("HTTP Handler should be nil")
	}
}

func TestMonitorRoutezRace(t *testing.T) {
	resetPreviousHTTPConnections()
	srvAOpts := DefaultMonitorOptions()
	srvAOpts.Cluster.Port = -1
	srvA := RunServer(srvAOpts)
	defer srvA.Shutdown()

	srvBOpts := nextServerOpts(srvAOpts)
	srvBOpts.Routes = RoutesFromStr(fmt.Sprintf("nats://127.0.0.1:%d", srvA.ClusterAddr().Port))

	url := fmt.Sprintf("http://127.0.0.1:%d/", srvA.MonitorAddr().Port)
	doneCh := make(chan struct{})
	go func() {
		defer func() {
			doneCh <- struct{}{}
		}()
		for i := 0; i < 10; i++ {
			time.Sleep(10 * time.Millisecond)
			// Reset ports
			srvBOpts.Port = -1
			srvBOpts.Cluster.Port = -1
			srvB := RunServer(srvBOpts)
			time.Sleep(20 * time.Millisecond)
			srvB.Shutdown()
		}
	}()
	done := false
	for !done {
		if resp, err := http.Get(url + "routez"); err != nil {
			time.Sleep(10 * time.Millisecond)
		} else {
			resp.Body.Close()
		}
		select {
		case <-doneCh:
			done = true
		default:
		}
	}
}

func TestConnzTLSInHandshake(t *testing.T) {
	resetPreviousHTTPConnections()

	tc := &TLSConfigOpts{}
	tc.CertFile = "configs/certs/server.pem"
	tc.KeyFile = "configs/certs/key.pem"

	var err error
	opts := DefaultMonitorOptions()
	opts.TLSTimeout = 1.5 // 1.5 seconds
	opts.TLSConfig, err = GenTLSConfig(tc)
	if err != nil {
		t.Fatalf("Error creating TSL config: %v", err)
	}

	s := RunServer(opts)
	defer s.Shutdown()

	// Create bare TCP connection to delay client TLS handshake
	c, err := net.Dial("tcp", fmt.Sprintf("%s:%d", opts.Host, opts.Port))
	if err != nil {
		t.Fatalf("Error on dial: %v", err)
	}
	defer c.Close()

	// Wait for the connection to be registered
	checkClientsCount(t, s, 1)

	start := time.Now()
	endpoint := fmt.Sprintf("http://%s:%d/connz", opts.HTTPHost, s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		connz := pollConz(t, s, mode, endpoint, nil)
		duration := time.Since(start)
		if duration >= 1500*time.Millisecond {
			t.Fatalf("Looks like connz blocked on handshake, took %v", duration)
		}
		if len(connz.Conns) != 1 {
			t.Fatalf("Expected 1 conn, got %v", len(connz.Conns))
		}
		conn := connz.Conns[0]
		// TLS fields should be not set
		if conn.TLSVersion != "" || conn.TLSCipher != "" {
			t.Fatalf("Expected TLS fields to not be set, got version:%v cipher:%v", conn.TLSVersion, conn.TLSCipher)
		}
	}
}

func TestServerIDs(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	murl := fmt.Sprintf("http://127.0.0.1:%d/", s.MonitorAddr().Port)

	for mode := 0; mode < 2; mode++ {
		v := pollVarz(t, s, mode, murl+"varz", nil)
		if v.ID == "" {
			t.Fatal("Varz ID is empty")
		}
		c := pollConz(t, s, mode, murl+"connz", nil)
		if c.ID == "" {
			t.Fatal("Connz ID is empty")
		}
		r := pollRoutez(t, s, mode, murl+"routez", nil)
		if r.ID == "" {
			t.Fatal("Routez ID is empty")
		}
		if v.ID != c.ID || v.ID != r.ID {
			t.Fatalf("Varz ID [%s] is not equal to Connz ID [%s] or Routez ID [%s]", v.ID, c.ID, r.ID)
		}
	}
}

func TestHttpStatsNoUpdatedWhenUsingServerFuncs(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	for i := 0; i < 10; i++ {
		s.Varz(nil)
		s.Connz(nil)
		s.Routez(nil)
		s.Subsz(nil)
	}

	v, _ := s.Varz(nil)
	endpoints := []string{VarzPath, ConnzPath, RoutezPath, SubszPath}
	for _, e := range endpoints {
		stats := v.HTTPReqStats[e]
		if stats != 0 {
			t.Fatalf("Expected HTTPReqStats for %q to be 0, got %v", e, stats)
		}
	}
}

func TestClusterEmptyWhenNotDefined(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	body := readBody(t, fmt.Sprintf("http://127.0.0.1:%d/varz", s.MonitorAddr().Port))
	var v map[string]interface{}
	if err := json.Unmarshal(body, &v); err != nil {
		stackFatalf(t, "Got an error unmarshalling the body: %v\n", err)
	}
	// Cluster can empty, or be defined but that needs to be empty.
	c, ok := v["cluster"]
	if !ok {
		return
	}
	if len(c.(map[string]interface{})) != 0 {
		t.Fatalf("Expected an empty cluster definition, instead got %+v\n", c)
	}
}

func TestRoutezPermissions(t *testing.T) {
	resetPreviousHTTPConnections()
	opts := DefaultMonitorOptions()
	opts.Cluster.Host = "127.0.0.1"
	opts.Cluster.Port = -1
	opts.Cluster.Permissions = &RoutePermissions{
		Import: &SubjectPermission{
			Allow: []string{"foo"},
		},
		Export: &SubjectPermission{
			Allow: []string{"*"},
			Deny:  []string{"foo", "nats"},
		},
	}

	s1 := RunServer(opts)
	defer s1.Shutdown()

	opts = DefaultMonitorOptions()
	opts.Cluster.Host = "127.0.0.1"
	opts.Cluster.Port = -1
	routeURL, _ := url.Parse(fmt.Sprintf("nats-route://127.0.0.1:%d", s1.ClusterAddr().Port))
	opts.Routes = []*url.URL{routeURL}
	opts.HTTPPort = -1

	s2 := RunServer(opts)
	defer s2.Shutdown()

	checkClusterFormed(t, s1, s2)

	urls := []string{
		fmt.Sprintf("http://127.0.0.1:%d/routez", s1.MonitorAddr().Port),
		fmt.Sprintf("http://127.0.0.1:%d/routez", s2.MonitorAddr().Port),
	}
	servers := []*Server{s1, s2}

	for i, url := range urls {
		for mode := 0; mode < 2; mode++ {
			rz := pollRoutez(t, servers[i], mode, url, nil)
			// For server 1, we expect to see imports and exports
			if i == 0 {
				if rz.Import == nil || rz.Import.Allow == nil ||
					len(rz.Import.Allow) != 1 || rz.Import.Allow[0] != "foo" ||
					rz.Import.Deny != nil {
					t.Fatalf("Unexpected Import %v", rz.Import)
				}
				if rz.Export == nil || rz.Export.Allow == nil || rz.Export.Deny == nil ||
					len(rz.Export.Allow) != 1 || rz.Export.Allow[0] != "*" ||
					len(rz.Export.Deny) != 2 || rz.Export.Deny[0] != "foo" || rz.Export.Deny[1] != "nats" {
					t.Fatalf("Unexpected Export %v", rz.Export)
				}
			} else {
				// We expect to see NO imports and exports for server B by default.
				if rz.Import != nil {
					t.Fatal("Routez body should NOT contain \"import\" information.")
				}
				if rz.Export != nil {
					t.Fatal("Routez body should NOT contain \"export\" information.")
				}
				// We do expect to see them show up for the information we have on Server A though.
				if len(rz.Routes) != 1 {
					t.Fatalf("Expected route array of 1, got %v\n", len(rz.Routes))
				}
				route := rz.Routes[0]
				if route.Import == nil || route.Import.Allow == nil ||
					len(route.Import.Allow) != 1 || route.Import.Allow[0] != "foo" ||
					route.Import.Deny != nil {
					t.Fatalf("Unexpected Import %v", route.Import)
				}
				if route.Export == nil || route.Export.Allow == nil || route.Export.Deny == nil ||
					len(route.Export.Allow) != 1 || route.Export.Allow[0] != "*" ||
					len(route.Export.Deny) != 2 || route.Export.Deny[0] != "foo" || route.Export.Deny[1] != "nats" {
					t.Fatalf("Unexpected Export %v", route.Export)
				}
			}
		}
	}
}

// Benchmark our Connz generation. Don't use HTTP here, just measure server endpoint.
func Benchmark_Connz(b *testing.B) {
	runtime.MemProfileRate = 0

	s := runMonitorServerNoHTTPPort()
	defer s.Shutdown()

	opts := s.getOpts()
	url := fmt.Sprintf("nats://%s:%d", opts.Host, opts.Port)

	// Create 250 connections with 100 subs each.
	for i := 0; i < 250; i++ {
		nc, err := nats.Connect(url)
		if err != nil {
			b.Fatalf("Error on connection[%d] to %s: %v", i, url, err)
		}
		for x := 0; x < 100; x++ {
			subj := fmt.Sprintf("foo.%d", x)
			nc.Subscribe(subj, func(m *nats.Msg) {})
		}
		nc.Flush()
		defer nc.Close()
	}

	b.ResetTimer()
	runtime.MemProfileRate = 1

	copts := &ConnzOptions{Subscriptions: false}
	for i := 0; i < b.N; i++ {
		_, err := s.Connz(copts)
		if err != nil {
			b.Fatalf("Error on Connz(): %v", err)
		}
	}
}

func Benchmark_Varz(b *testing.B) {
	runtime.MemProfileRate = 0

	s := runMonitorServerNoHTTPPort()
	defer s.Shutdown()

	b.ResetTimer()
	runtime.MemProfileRate = 1

	for i := 0; i < b.N; i++ {
		_, err := s.Varz(nil)
		if err != nil {
			b.Fatalf("Error on Connz(): %v", err)
		}
	}
}

func Benchmark_VarzHttp(b *testing.B) {
	runtime.MemProfileRate = 0

	s := runMonitorServer()
	defer s.Shutdown()

	murl := fmt.Sprintf("http://127.0.0.1:%d/varz", s.MonitorAddr().Port)

	b.ResetTimer()
	runtime.MemProfileRate = 1

	for i := 0; i < b.N; i++ {
		v := &Varz{}
		resp, err := http.Get(murl)
		if err != nil {
			b.Fatalf("Expected no error: Got %v\n", err)
		}
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			b.Fatalf("Got an error reading the body: %v\n", err)
		}
		if err := json.Unmarshal(body, v); err != nil {
			b.Fatalf("Got an error unmarshalling the body: %v\n", err)
		}
		resp.Body.Close()
	}
}

func TestVarzRaces(t *testing.T) {
	s := runMonitorServer()
	defer s.Shutdown()

	murl := fmt.Sprintf("http://127.0.0.1:%d/varz", s.MonitorAddr().Port)
	done := make(chan struct{})
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			for i := 0; i < 2; i++ {
				v := pollVarz(t, s, i, murl, nil)
				// Check the field that we are setting in main thread
				// to ensure that we have a copy and there is no
				// race with fields set in s.info and s.opts
				if v.ID == "abc" || v.MaxConn == -1 {
					// We will not get there. Need to have something
					// otherwise staticcheck will report empty branch
					return
				}

				select {
				case <-done:
					return
				default:
				}
			}
		}
	}()

	for i := 0; i < 1000; i++ {
		// Simulate a change in server's info and options
		// by changing something.
		s.mu.Lock()
		s.info.ID = fmt.Sprintf("serverid_%d", i)
		s.opts.MaxConn = 100 + i
		s.mu.Unlock()
		time.Sleep(time.Nanosecond)
	}
	close(done)
	wg.Wait()

	// Now check that there is no race doing parallel polling
	wg.Add(3)
	done = make(chan struct{})
	poll := func() {
		defer wg.Done()
		for {
			for mode := 0; mode < 2; mode++ {
				pollVarz(t, s, mode, murl, nil)
			}
			select {
			case <-done:
				return
			default:
			}
		}
	}
	for i := 0; i < 3; i++ {
		go poll()
	}
	time.Sleep(500 * time.Millisecond)
	close(done)
	wg.Wait()
}

func testMonitorStructPresent(t *testing.T, tag string) {
	t.Helper()

	resetPreviousHTTPConnections()
	opts := DefaultMonitorOptions()
	s := RunServer(opts)
	defer s.Shutdown()

	varzURL := fmt.Sprintf("http://127.0.0.1:%d/varz", s.MonitorAddr().Port)
	body := readBody(t, varzURL)
	if !bytes.Contains(body, []byte(`"`+tag+`": {}`)) {
		t.Fatalf("%s should be present and empty, got %s", tag, body)
	}
}

func TestMonitorCluster(t *testing.T) {
	testMonitorStructPresent(t, "cluster")

	resetPreviousHTTPConnections()
	opts := DefaultMonitorOptions()
	opts.Cluster.Port = -1
	opts.Cluster.AuthTimeout = 1
	opts.Routes = RoutesFromStr("nats://127.0.0.1:1234")
	s := RunServer(opts)
	defer s.Shutdown()

	expected := ClusterOptsVarz{
		opts.Cluster.Host,
		opts.Cluster.Port,
		opts.Cluster.AuthTimeout,
		[]string{"127.0.0.1:1234"},
	}

	varzURL := fmt.Sprintf("http://127.0.0.1:%d/varz", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		check := func(t *testing.T, v *Varz) {
			t.Helper()
			if !reflect.DeepEqual(v.Cluster, expected) {
				t.Fatalf("mode=%v - expected %+v, got %+v", mode, expected, v.Cluster)
			}
		}
		v := pollVarz(t, s, mode, varzURL, nil)
		check(t, v)

		// Having this here to make sure that if fields are added in ClusterOptsVarz,
		// we make sure to update this test (compiler will report an error if we don't)
		_ = ClusterOptsVarz{"", 0, 0, nil}

		// Alter the fields to make sure that we have a proper deep copy
		// of what may be stored in the server. Anything we change here
		// should not affect the next returned value.
		v.Cluster.Host = "wrong"
		v.Cluster.Port = 0
		v.Cluster.AuthTimeout = 0
		v.Cluster.URLs = []string{"wrong"}
		v = pollVarz(t, s, mode, varzURL, nil)
		check(t, v)
	}
}

func TestMonitorClusterURLs(t *testing.T) {
	resetPreviousHTTPConnections()

	o2 := DefaultOptions()
	o2.Cluster.Host = "127.0.0.1"
	s2 := RunServer(o2)
	defer s2.Shutdown()

	s2ClusterHostPort := fmt.Sprintf("127.0.0.1:%d", s2.ClusterAddr().Port)

	template := `
		port: -1
		http: -1
		cluster: {
			port: -1
			routes [
				%s
				%s
			]
		}
	`
	conf := createConfFile(t, []byte(fmt.Sprintf(template, "nats://"+s2ClusterHostPort, "")))
	defer os.Remove(conf)
	s1, _ := RunServerWithConfig(conf)
	defer s1.Shutdown()

	checkClusterFormed(t, s1, s2)

	// Check /varz cluster{} to see the URLs from s1 to s2
	varzURL := fmt.Sprintf("http://127.0.0.1:%d/varz", s1.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		v := pollVarz(t, s1, mode, varzURL, nil)
		if n := len(v.Cluster.URLs); n != 1 {
			t.Fatalf("mode=%v - Expected 1 URL, got %v", mode, n)
		}
		if v.Cluster.URLs[0] != s2ClusterHostPort {
			t.Fatalf("mode=%v - Expected url %q, got %q", mode, s2ClusterHostPort, v.Cluster.URLs[0])
		}
	}

	otherClusterHostPort := "127.0.0.1:1234"
	// Now update the config and add a route
	changeCurrentConfigContentWithNewContent(t, conf, []byte(fmt.Sprintf(template, "nats://"+s2ClusterHostPort, "nats://"+otherClusterHostPort)))

	if err := s1.Reload(); err != nil {
		t.Fatalf("Error on reload: %v", err)
	}

	// Verify cluster still ok
	checkClusterFormed(t, s1, s2)

	// Now verify that s1 reports in /varz the new URL
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		for mode := 0; mode < 2; mode++ {
			v := pollVarz(t, s1, mode, varzURL, nil)
			if n := len(v.Cluster.URLs); n != 2 {
				t.Fatalf("mode=%v - Expected 2 URL, got %v", mode, n)
			}
			gotS2 := false
			gotOther := false
			for _, u := range v.Cluster.URLs {
				if u == s2ClusterHostPort {
					gotS2 = true
				} else if u == otherClusterHostPort {
					gotOther = true
				} else {
					t.Fatalf("mode=%v - Incorrect url: %q", mode, u)
				}
			}
			if !gotS2 {
				t.Fatalf("mode=%v - Did not get cluster URL for s2", mode)
			}
			if !gotOther {
				t.Fatalf("mode=%v - Did not get the new cluster URL", mode)
			}
		}
		return nil
	})

	// Remove all routes from config
	changeCurrentConfigContentWithNewContent(t, conf, []byte(fmt.Sprintf(template, "", "")))

	if err := s1.Reload(); err != nil {
		t.Fatalf("Error on reload: %v", err)
	}

	// Now verify that s1 reports no ULRs in /varz
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		for mode := 0; mode < 2; mode++ {
			v := pollVarz(t, s1, mode, varzURL, nil)
			if n := len(v.Cluster.URLs); n != 0 {
				t.Fatalf("mode=%v - Expected 0 URL, got %v", mode, n)
			}
		}
		return nil
	})
}

func TestMonitorGateway(t *testing.T) {
	testMonitorStructPresent(t, "gateway")

	resetPreviousHTTPConnections()
	opts := DefaultMonitorOptions()
	opts.Gateway.Name = "A"
	opts.Gateway.Port = -1
	opts.Gateway.AuthTimeout = 1
	opts.Gateway.TLSTimeout = 1
	opts.Gateway.Advertise = "127.0.0.1"
	opts.Gateway.ConnectRetries = 1
	opts.Gateway.RejectUnknown = false
	u1, _ := url.Parse("nats://ivan:pwd@localhost:1234")
	u2, _ := url.Parse("nats://localhost:1235")
	opts.Gateway.Gateways = []*RemoteGatewayOpts{
		&RemoteGatewayOpts{
			Name:       "B",
			TLSTimeout: 1,
			URLs: []*url.URL{
				u1,
				u2,
			},
		},
	}
	s := RunServer(opts)
	defer s.Shutdown()

	expected := GatewayOptsVarz{
		"A",
		opts.Gateway.Host,
		opts.Gateway.Port,
		opts.Gateway.AuthTimeout,
		opts.Gateway.TLSTimeout,
		opts.Gateway.Advertise,
		opts.Gateway.ConnectRetries,
		[]RemoteGatewayOptsVarz{{"B", 1, nil}},
		opts.Gateway.RejectUnknown,
	}
	// Since URLs array is not guaranteed to be always the same order,
	// we don't add it in the expected GatewayOptsVarz, instead we
	// maintain here.
	expectedURLs := []string{"localhost:1234", "localhost:1235"}

	varzURL := fmt.Sprintf("http://127.0.0.1:%d/varz", s.MonitorAddr().Port)
	for mode := 0; mode < 2; mode++ {
		check := func(t *testing.T, v *Varz) {
			t.Helper()
			var urls []string
			if len(v.Gateway.Gateways) == 1 {
				urls = v.Gateway.Gateways[0].URLs
				v.Gateway.Gateways[0].URLs = nil
			}
			if !reflect.DeepEqual(v.Gateway, expected) {
				t.Fatalf("mode=%v - expected %+v, got %+v", mode, expected, v.Gateway)
			}
			// Now compare urls
			for _, u := range expectedURLs {
				ok := false
				for _, u2 := range urls {
					if u == u2 {
						ok = true
						break
					}
				}
				if !ok {
					t.Fatalf("mode=%v - expected urls to be %v, got %v", mode, expected.Gateways[0].URLs, urls)
				}
			}
		}
		v := pollVarz(t, s, mode, varzURL, nil)
		check(t, v)

		// Having this here to make sure that if fields are added in GatewayOptsVarz,
		// we make sure to update this test (compiler will report an error if we don't)
		_ = GatewayOptsVarz{"", "", 0, 0, 0, "", 0, []RemoteGatewayOptsVarz{{"", 0, nil}}, false}

		// Alter the fields to make sure that we have a proper deep copy
		// of what may be stored in the server. Anything we change here
		// should not affect the next returned value.
		v.Gateway.Name = "wrong"
		v.Gateway.Host = "wrong"
		v.Gateway.Port = 0
		v.Gateway.AuthTimeout = 1234.5
		v.Gateway.TLSTimeout = 1234.5
		v.Gateway.Advertise = "wrong"
		v.Gateway.ConnectRetries = 1234
		v.Gateway.Gateways[0].Name = "wrong"
		v.Gateway.Gateways[0].TLSTimeout = 1234.5
		v.Gateway.Gateways[0].URLs = []string{"wrong"}
		v.Gateway.RejectUnknown = true
		v = pollVarz(t, s, mode, varzURL, nil)
		check(t, v)
	}
}

func TestMonitorGatewayURLsUpdated(t *testing.T) {
	resetPreviousHTTPConnections()

	ob1 := testDefaultOptionsForGateway("B")
	sb1 := runGatewayServer(ob1)
	defer sb1.Shutdown()

	// Start a1 that has a single URL to sb1.
	oa := testGatewayOptionsFromToWithServers(t, "A", "B", sb1)
	oa.HTTPHost = "127.0.0.1"
	oa.HTTPPort = MONITOR_PORT
	sa := runGatewayServer(oa)
	defer sa.Shutdown()

	waitForOutboundGateways(t, sa, 1, 2*time.Second)

	varzURL := fmt.Sprintf("http://127.0.0.1:%d/varz", sa.MonitorAddr().Port)
	// Check the /varz gateway's URLs
	for mode := 0; mode < 2; mode++ {
		v := pollVarz(t, sa, mode, varzURL, nil)
		if n := len(v.Gateway.Gateways); n != 1 {
			t.Fatalf("mode=%v - Expected 1 remote gateway, got %v", mode, n)
		}
		gw := v.Gateway.Gateways[0]
		if n := len(gw.URLs); n != 1 {
			t.Fatalf("mode=%v - Expected 1 url, got %v", mode, n)
		}
		expected := oa.Gateway.Gateways[0].URLs[0].Host
		if u := gw.URLs[0]; u != expected {
			t.Fatalf("mode=%v - Expected URL %q, got %q", mode, expected, u)
		}
	}

	// Now start sb2 that clusters with sb1. sa should add to its list of URLs
	// sb2 gateway's connect URL.
	ob2 := testDefaultOptionsForGateway("B")
	ob2.Routes = RoutesFromStr(fmt.Sprintf("nats://127.0.0.1:%d", sb1.ClusterAddr().Port))
	sb2 := runGatewayServer(ob2)
	defer sb2.Shutdown()

	// Wait for sb1 and sb2 to connect
	checkClusterFormed(t, sb1, sb2)
	// sb2 should be made aware of gateway A and connect to sa
	waitForInboundGateways(t, sa, 2, 2*time.Second)
	// Now check that URLs in /varz get updated
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		for mode := 0; mode < 2; mode++ {
			v := pollVarz(t, sa, mode, varzURL, nil)
			if n := len(v.Gateway.Gateways); n != 1 {
				return fmt.Errorf("mode=%v - Expected 1 remote gateway, got %v", mode, n)
			}
			gw := v.Gateway.Gateways[0]
			if n := len(gw.URLs); n != 2 {
				return fmt.Errorf("mode=%v - Expected 2 urls, got %v", mode, n)
			}

			gotSB1 := false
			gotSB2 := false
			for _, u := range gw.URLs {
				if u == fmt.Sprintf("127.0.0.1:%d", sb1.GatewayAddr().Port) {
					gotSB1 = true
				} else if u == fmt.Sprintf("127.0.0.1:%d", sb2.GatewayAddr().Port) {
					gotSB2 = true
				} else {
					return fmt.Errorf("mode=%v - Incorrect URL to gateway B: %v", mode, u)
				}
			}
			if !gotSB1 {
				return fmt.Errorf("mode=%v - Did not get URL to sb1", mode)
			}
			if !gotSB2 {
				return fmt.Errorf("mode=%v - Did not get URL to sb2", mode)
			}
		}
		return nil
	})
}

func TestMonitorLeafNode(t *testing.T) {
	testMonitorStructPresent(t, "leaf")

	resetPreviousHTTPConnections()
	opts := DefaultMonitorOptions()
	opts.LeafNode.Port = -1
	opts.LeafNode.AuthTimeout = 1
	opts.LeafNode.TLSTimeout = 1
	u, _ := url.Parse("nats://ivan:pwd@localhost:1234")
	opts.LeafNode.Remotes = []*RemoteLeafOpts{
		&RemoteLeafOpts{
			LocalAccount: "acc",
			URL:          u,
			TLSTimeout:   1,
		},
	}
	s := RunServer(opts)
	defer s.Shutdown()

	expected := LeafNodeOptsVarz{
		opts.LeafNode.Host,
		opts.LeafNode.Port,
		opts.LeafNode.AuthTimeout,
		opts.LeafNode.TLSTimeout,
		[]RemoteLeafOptsVarz{
			{
				"acc",
				"localhost:1234",
				1,
			},
		},
	}

	varzURL := fmt.Sprintf("http://127.0.0.1:%d/varz", s.MonitorAddr().Port)

	for mode := 0; mode < 2; mode++ {
		check := func(t *testing.T, v *Varz) {
			t.Helper()
			if !reflect.DeepEqual(v.LeafNode, expected) {
				t.Fatalf("mode=%v - expected %+v, got %+v", mode, expected, v.LeafNode)
			}
		}
		v := pollVarz(t, s, mode, varzURL, nil)
		check(t, v)

		// Having this here to make sure that if fields are added in ClusterOptsVarz,
		// we make sure to update this test (compiler will report an error if we don't)
		_ = LeafNodeOptsVarz{"", 0, 0, 0, []RemoteLeafOptsVarz{{"", "", 0}}}

		// Alter the fields to make sure that we have a proper deep copy
		// of what may be stored in the server. Anything we change here
		// should not affect the next returned value.
		v.LeafNode.Host = "wrong"
		v.LeafNode.Port = 0
		v.LeafNode.AuthTimeout = 1234.5
		v.LeafNode.TLSTimeout = 1234.5
		v.LeafNode.Remotes[0].LocalAccount = "wrong"
		v.LeafNode.Remotes[0].URL = "wrong"
		v.LeafNode.Remotes[0].TLSTimeout = 1234.5
		v = pollVarz(t, s, mode, varzURL, nil)
		check(t, v)
	}
}

func pollGatewayz(t *testing.T, s *Server, mode int, url string, opts *GatewayzOptions) *Gatewayz {
	t.Helper()
	if mode == 0 {
		g := &Gatewayz{}
		body := readBody(t, url)
		if err := json.Unmarshal(body, g); err != nil {
			t.Fatalf("Got an error unmarshalling the body: %v\n", err)
		}
		return g
	}
	g, err := s.Gatewayz(opts)
	if err != nil {
		t.Fatalf("Error on Gatewayz: %v", err)
	}
	return g
}

func TestMonitorGatewayz(t *testing.T) {
	resetPreviousHTTPConnections()

	// First check that without gateway configured
	s := runMonitorServer()
	defer s.Shutdown()
	url := fmt.Sprintf("http://127.0.0.1:%d/gatewayz", s.MonitorAddr().Port)
	for pollMode := 0; pollMode < 2; pollMode++ {
		g := pollGatewayz(t, s, pollMode, url, nil)
		// Expect Name and port to be empty
		if g.Name != _EMPTY_ || g.Port != 0 {
			t.Fatalf("Expected no gateway, got %+v", g)
		}
	}
	s.Shutdown()

	ob1 := testDefaultOptionsForGateway("B")
	sb1 := runGatewayServer(ob1)
	defer sb1.Shutdown()

	// Start a1 that has a single URL to sb1.
	oa := testGatewayOptionsFromToWithServers(t, "A", "B", sb1)
	oa.HTTPHost = "127.0.0.1"
	oa.HTTPPort = MONITOR_PORT
	sa := runGatewayServer(oa)
	defer sa.Shutdown()

	waitForOutboundGateways(t, sa, 1, 2*time.Second)
	waitForInboundGateways(t, sa, 1, 2*time.Second)

	waitForOutboundGateways(t, sb1, 1, 2*time.Second)
	waitForInboundGateways(t, sb1, 1, 2*time.Second)

	gatewayzURL := fmt.Sprintf("http://127.0.0.1:%d/gatewayz", sa.MonitorAddr().Port)
	for pollMode := 0; pollMode < 2; pollMode++ {
		g := pollGatewayz(t, sa, pollMode, gatewayzURL, nil)
		if g.Host != oa.Gateway.Host {
			t.Fatalf("mode=%v - Expected host to be %q, got %q", pollMode, oa.Gateway.Host, g.Host)
		}
		if g.Port != oa.Gateway.Port {
			t.Fatalf("mode=%v - Expected port to be %v, got %v", pollMode, oa.Gateway.Port, g.Port)
		}
		if n := len(g.OutboundGateways); n != 1 {
			t.Fatalf("mode=%v - Expected outbound to 1 gateway, got %v", pollMode, n)
		}
		if n := len(g.InboundGateways); n != 1 {
			t.Fatalf("mode=%v - Expected inbound from 1 gateway, got %v", pollMode, n)
		}
		og := g.OutboundGateways["B"]
		if og == nil {
			t.Fatalf("mode=%v - Expected to find outbound connection to B, got none", pollMode)
		}
		if !og.IsConfigured {
			t.Fatalf("mode=%v - Expected gw connection to be configured, was not", pollMode)
		}
		if og.Connection == nil {
			t.Fatalf("mode=%v - Expected outbound connection to B to be set, wat not", pollMode)
		}
		if og.Connection.Name != sb1.ID() {
			t.Fatalf("mode=%v - Expected outbound connection to B to have name %q, got %q", pollMode, sb1.ID(), og.Connection.Name)
		}
		if n := len(og.Accounts); n != 0 {
			t.Fatalf("mode=%v - Expected no account, got %v", pollMode, n)
		}
		ig := g.InboundGateways["B"]
		if ig == nil {
			t.Fatalf("mode=%v - Expected to find inbound connection from B, got none", pollMode)
		}
		if n := len(ig); n != 1 {
			t.Fatalf("mode=%v - Expected 1 inbound connection, got %v", pollMode, n)
		}
		igc := ig[0]
		if igc.Connection == nil {
			t.Fatalf("mode=%v - Expected inbound connection to B to be set, wat not", pollMode)
		}
		if igc.Connection.Name != sb1.ID() {
			t.Fatalf("mode=%v - Expected inbound connection to B to have name %q, got %q", pollMode, sb1.ID(), igc.Connection.Name)
		}
	}

	// Now start sb2 that clusters with sb1. sa should add to its list of URLs
	// sb2 gateway's connect URL.
	ob2 := testDefaultOptionsForGateway("B")
	ob2.Routes = RoutesFromStr(fmt.Sprintf("nats://127.0.0.1:%d", sb1.ClusterAddr().Port))
	sb2 := runGatewayServer(ob2)

	// Wait for sb1 and sb2 to connect
	checkClusterFormed(t, sb1, sb2)
	// sb2 should be made aware of gateway A and connect to sa
	waitForInboundGateways(t, sa, 2, 2*time.Second)
	// Now check that URLs in /varz get updated
	checkGatewayB := func(t *testing.T, url string, opts *GatewayzOptions) {
		t.Helper()
		checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
			for pollMode := 0; pollMode < 2; pollMode++ {
				g := pollGatewayz(t, sa, pollMode, url, opts)
				if n := len(g.OutboundGateways); n != 1 {
					t.Fatalf("mode=%v - Expected outbound to 1 gateway, got %v", pollMode, n)
				}
				// The InboundGateways is a map with key the gateway names,
				// then value is array of connections. So should be 1 here.
				if n := len(g.InboundGateways); n != 1 {
					t.Fatalf("mode=%v - Expected inbound from 1 gateway, got %v", pollMode, n)
				}
				ig := g.InboundGateways["B"]
				if ig == nil {
					t.Fatalf("mode=%v - Expected to find inbound connection from B, got none", pollMode)
				}
				if n := len(ig); n != 2 {
					t.Fatalf("mode=%v - Expected 2 inbound connections from gateway B, got %v", pollMode, n)
				}
				gotSB1 := false
				gotSB2 := false
				for _, rg := range ig {
					if rg.Connection != nil {
						if rg.Connection.Name == sb1.ID() {
							gotSB1 = true
						} else if rg.Connection.Name == sb2.ID() {
							gotSB2 = true
						}
					}
				}
				if !gotSB1 {
					t.Fatalf("mode=%v - Missing inbound connection from sb1", pollMode)
				}
				if !gotSB2 {
					t.Fatalf("mode=%v - Missing inbound connection from sb2", pollMode)
				}
			}
			return nil
		})
	}
	checkGatewayB(t, gatewayzURL, nil)

	// Start a new cluser C that connects to B. A should see it as
	// a non-configured gateway.
	oc := testGatewayOptionsFromToWithServers(t, "C", "B", sb1)
	sc := runGatewayServer(oc)
	defer sc.Shutdown()

	// All servers should have 2 outbound connections (one for each other cluster)
	waitForOutboundGateways(t, sa, 2, 2*time.Second)
	waitForOutboundGateways(t, sb1, 2, 2*time.Second)
	waitForOutboundGateways(t, sb2, 2, 2*time.Second)
	waitForOutboundGateways(t, sc, 2, 2*time.Second)

	// Server sa should have 3 inbounds now
	waitForInboundGateways(t, sa, 3, 2*time.Second)

	// Check gatewayz again to see that we have C now.
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		for pollMode := 0; pollMode < 2; pollMode++ {
			g := pollGatewayz(t, sa, pollMode, gatewayzURL, nil)
			if n := len(g.OutboundGateways); n != 2 {
				t.Fatalf("mode=%v - Expected outbound to 2 gateways, got %v", pollMode, n)
			}
			// The InboundGateways is a map with key the gateway names,
			// then value is array of connections. So should be 2 here.
			if n := len(g.InboundGateways); n != 2 {
				t.Fatalf("mode=%v - Expected inbound from 2 gateways, got %v", pollMode, n)
			}
			og := g.OutboundGateways["C"]
			if og == nil {
				t.Fatalf("mode=%v - Expected to find outbound connection to C, got none", pollMode)
			}
			if og.IsConfigured {
				t.Fatalf("mode=%v - Expected IsConfigured for gateway C to be false, was true", pollMode)
			}
			if og.Connection == nil {
				t.Fatalf("mode=%v - Expected connection to C, got none", pollMode)
			}
			if og.Connection.Name != sc.ID() {
				t.Fatalf("mode=%v - Expected outbound connection to C to have name %q, got %q", pollMode, sc.ID(), og.Connection.Name)
			}
			ig := g.InboundGateways["C"]
			if ig == nil {
				t.Fatalf("mode=%v - Expected to find inbound connection from C, got none", pollMode)
			}
			if n := len(ig); n != 1 {
				t.Fatalf("mode=%v - Expected 1 inbound connections from gateway C, got %v", pollMode, n)
			}
			igc := ig[0]
			if igc.Connection == nil {
				t.Fatalf("mode=%v - Expected connection to C, got none", pollMode)
			}
			if igc.Connection.Name != sc.ID() {
				t.Fatalf("mode=%v - Expected outbound connection to C to have name %q, got %q", pollMode, sc.ID(), og.Connection.Name)
			}
		}
		return nil
	})

	// Select only 1 gateway by passing the name to option/url
	opts := &GatewayzOptions{Name: "B"}
	checkGatewayB(t, gatewayzURL+"?gw_name=B", opts)

	// Stop gateway C and check that we have only B, with and without filter.
	sc.Shutdown()
	checkGatewayB(t, gatewayzURL+"?gw_name=B", opts)
	checkGatewayB(t, gatewayzURL, nil)
}

func TestMonitorGatewayzAccounts(t *testing.T) {
	resetPreviousHTTPConnections()

	// Create bunch of Accounts
	totalAccounts := gwAccountsLimit + 5
	accounts := ""
	for i := 0; i < totalAccounts; i++ {
		acc := fmt.Sprintf("	acc_%d: { users=[{user:user_%d, password: pwd}] }\n", i, i)
		accounts += acc
	}

	bConf := createConfFile(t, []byte(fmt.Sprintf(`
		accounts {
			%s
		}
		port: -1
		http: -1
		gateway: {
			name: "B"
			port: -1
		}
	`, accounts)))
	defer os.Remove(bConf)

	sb, ob := RunServerWithConfig(bConf)
	defer sb.Shutdown()
	sb.SetLogger(&DummyLogger{}, true, true)

	// Start a1 that has a single URL to sb1.
	aConf := createConfFile(t, []byte(fmt.Sprintf(`
		accounts {
			%s
		}
		port: -1
		http: -1
		gateway: {
			name: "A"
			port: -1
			gateways [
				{
					name: "B"
					url: "nats://127.0.0.1:%d"
				}
			]
		}
	`, accounts, sb.GatewayAddr().Port)))
	defer os.Remove(aConf)

	sa, oa := RunServerWithConfig(aConf)
	defer sa.Shutdown()
	sa.SetLogger(&DummyLogger{}, true, true)

	waitForOutboundGateways(t, sa, 1, 2*time.Second)
	waitForInboundGateways(t, sa, 1, 2*time.Second)
	waitForOutboundGateways(t, sb, 1, 2*time.Second)
	waitForInboundGateways(t, sb, 1, 2*time.Second)

	// Create clients for each account on A and publish a message
	// so that list of accounts appear in gatewayz
	produceMsgsFromA := func(t *testing.T) {
		t.Helper()
		for i := 0; i < totalAccounts; i++ {
			nc, err := nats.Connect(fmt.Sprintf("nats://user_%d:pwd@%s:%d", i, oa.Host, oa.Port))
			if err != nil {
				t.Fatalf("Error on connect: %v", err)
			}
			nc.Publish("foo", []byte("hello"))
			nc.Flush()
			nc.Close()
		}
	}
	produceMsgsFromA(t)

	// Wait for A- for all accounts
	gwc := sa.getOutboundGatewayConnection("B")
	for i := 0; i < totalAccounts; i++ {
		checkForAccountNoInterest(t, gwc, fmt.Sprintf("acc_%d", i), true, 2*time.Second)
	}

	// Check accounts...
	gatewayzURL := fmt.Sprintf("http://127.0.0.1:%d/gatewayz", sa.MonitorAddr().Port)
	for pollMode := 0; pollMode < 2; pollMode++ {
		// First, without asking for it, they should not be present.
		g := pollGatewayz(t, sa, pollMode, gatewayzURL, nil)
		og := g.OutboundGateways["B"]
		if og == nil {
			t.Fatalf("mode=%v - Expected outbound gateway to B, got none", pollMode)
		}
		if n := len(og.Accounts); n != 0 {
			t.Fatalf("mode=%v - Expected accounts list to not be present by default, got %v", pollMode, n)
		}
		// Now ask for the accounts, but by default should be limited to gwAccountsLimit
		g = pollGatewayz(t, sa, pollMode, gatewayzURL+"?accs=1", &GatewayzOptions{Accounts: true})
		og = g.OutboundGateways["B"]
		if og == nil {
			t.Fatalf("mode=%v - Expected outbound gateway to B, got none", pollMode)
		}
		if n := len(og.Accounts); n != gwAccountsLimit {
			t.Fatalf("mode=%v - Expected accounts list to be limited to %v, got %v", pollMode, gwAccountsLimit, n)
		}
		// Ask for a given limit, make sure that we don't need to specify "includ accounts" option.
		limit := gwAccountsLimit - 3
		g = pollGatewayz(t, sa, pollMode, fmt.Sprintf(gatewayzURL+"?accs_limit=%d", limit),
			&GatewayzOptions{AccountsLimit: limit})
		og = g.OutboundGateways["B"]
		if og == nil {
			t.Fatalf("mode=%v - Expected outbound gateway to B, got none", pollMode)
		}
		if n := len(og.Accounts); n != limit {
			t.Fatalf("mode=%v - Expected to get %d accounts, got %v", pollMode, limit, n)
		}
		// Now if we ask for more than we actually have, we should get them all.
		g = pollGatewayz(t, sa, pollMode, fmt.Sprintf(gatewayzURL+"?accs=1&accs_limit=%d", totalAccounts+10),
			&GatewayzOptions{Accounts: true, AccountsLimit: totalAccounts + 10})
		og = g.OutboundGateways["B"]
		if og == nil {
			t.Fatalf("mode=%v - Expected outbound gateway to B, got none", pollMode)
		}
		if n := len(og.Accounts); n != totalAccounts {
			t.Fatalf("mode=%v - Expected to get all %d accounts, got %v", pollMode, totalAccounts, n)
		}
		// Now account details
		for _, acc := range og.Accounts {
			if acc.InterestMode != Optimistic.String() {
				t.Fatalf("mode=%v - Expected optimistic mode, got %q", pollMode, acc.InterestMode)
			}
			// Since there is no interest at all on B, the publish
			// will have resulted in total account no interest, so
			// the number of no interest (subject wise) should be 0
			if acc.NoInterestCount != 0 {
				t.Fatalf("mode=%v - Expected 0 no-interest, got %v", pollMode, acc.NoInterestCount)
			}
			if acc.NumQueueSubscriptions != 0 || acc.TotalSubscriptions != 0 {
				t.Fatalf("mode=%v - Expected total subs to be 0, got %v - and num queue subs to be 0, got %v",
					pollMode, acc.TotalSubscriptions, acc.NumQueueSubscriptions)
			}
		}
	}

	// Check inbound on B
	gwURLServerB := fmt.Sprintf("http://127.0.0.1:%d/gatewayz", sb.MonitorAddr().Port)
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		for pollMode := 0; pollMode < 2; pollMode++ {
			// First, without asking for it, they should not be present.
			g := pollGatewayz(t, sb, pollMode, gwURLServerB, nil)
			igs := g.InboundGateways["A"]
			if igs == nil {
				return fmt.Errorf("mode=%v - Expected inbound gateway to A, got none", pollMode)
			}
			if len(igs) != 1 {
				return fmt.Errorf("mode=%v - Expected single inbound, got %v", pollMode, len(igs))
			}
			ig := igs[0]
			if n := len(ig.Accounts); n != 0 {
				return fmt.Errorf("mode=%v - Expected no account, got %v", pollMode, n)
			}
			// Check that list of accounts is limited by default
			g = pollGatewayz(t, sb, pollMode, gwURLServerB+"?accs=1", &GatewayzOptions{Accounts: true})
			igs = g.InboundGateways["A"]
			if igs == nil {
				return fmt.Errorf("mode=%v - Expected inbound gateway to A, got none", pollMode)
			}
			if len(igs) != 1 {
				return fmt.Errorf("mode=%v - Expected single inbound, got %v", pollMode, len(igs))
			}
			ig = igs[0]
			if n := len(ig.Accounts); n != gwAccountsLimit {
				return fmt.Errorf("mode=%v - Expected to get %d accounts, got %v", pollMode, gwAccountsLimit, n)
			}
			// Ask for specific limit, ensure it works if "accs=1" is not specified
			limit := gwAccountsLimit - 3
			g = pollGatewayz(t, sb, pollMode, fmt.Sprintf(gwURLServerB+"?accs_limit=%d", limit),
				&GatewayzOptions{AccountsLimit: limit})
			igs = g.InboundGateways["A"]
			if igs == nil {
				return fmt.Errorf("mode=%v - Expected inbound gateway to A, got none", pollMode)
			}
			if len(igs) != 1 {
				return fmt.Errorf("mode=%v - Expected single inbound, got %v", pollMode, len(igs))
			}
			ig = igs[0]
			if n := len(ig.Accounts); n != limit {
				return fmt.Errorf("mode=%v - Expected to get %d accounts, got %v", pollMode, limit, n)
			}
			// Ask for more accounts that we know we have so we get them all
			g = pollGatewayz(t, sb, pollMode, fmt.Sprintf(gwURLServerB+"?accs=1&accs_limit=%d", totalAccounts+10),
				&GatewayzOptions{Accounts: true, AccountsLimit: totalAccounts + 10})
			igs = g.InboundGateways["A"]
			if igs == nil {
				return fmt.Errorf("mode=%v - Expected inbound gateway to A, got none", pollMode)
			}
			if len(igs) != 1 {
				return fmt.Errorf("mode=%v - Expected single inbound, got %v", pollMode, len(igs))
			}
			ig = igs[0]
			if ig.Connection == nil {
				return fmt.Errorf("mode=%v - Expected inbound connection from A to be set, wat not", pollMode)
			}
			if ig.Connection.Name != sa.ID() {
				t.Fatalf("mode=%v - Expected inbound connection from A to have name %q, got %q", pollMode, sa.ID(), ig.Connection.Name)
			}
			if n := len(ig.Accounts); n != totalAccounts {
				return fmt.Errorf("mode=%v - Expected to get all %d accounts, got %v", pollMode, totalAccounts, n)
			}
			// Now account details
			for _, acc := range ig.Accounts {
				if acc.InterestMode != Optimistic.String() {
					return fmt.Errorf("mode=%v - Expected optimistic mode, got %q", pollMode, acc.InterestMode)
				}
				// Since there is no interest at all on B, the publish
				// will have resulted in total account no interest, so
				// the number of no interest (subject wise) should be 0
				if acc.NoInterestCount != 0 {
					t.Fatalf("mode=%v - Expected 0 no-interest, got %v", pollMode, acc.NoInterestCount)
				}
				// For inbound gateway, NumQueueSubscriptions and TotalSubscriptions
				// are not relevant.
				if acc.NumQueueSubscriptions != 0 || acc.TotalSubscriptions != 0 {
					return fmt.Errorf("mode=%v - For inbound connection, expected num queue subs and total subs to be 0, got %v and %v",
						pollMode, acc.TotalSubscriptions, acc.NumQueueSubscriptions)
				}
			}
		}
		return nil
	})

	// Now create subscriptions on B to prevent A- and check on subject no interest
	for i := 0; i < totalAccounts; i++ {
		nc, err := nats.Connect(fmt.Sprintf("nats://user_%d:pwd@%s:%d", i, ob.Host, ob.Port))
		if err != nil {
			t.Fatalf("Error on connect: %v", err)
		}
		defer nc.Close()
		// Create a queue sub so it shows up in gatewayz
		nc.QueueSubscribeSync("bar", "queue")
		// Create plain subscriptions on baz.0, baz.1 and baz.2.
		// Create to for each subject. Since gateways will send
		// only once per subject, the number of subs should be 3, not 6.
		for j := 0; j < 3; j++ {
			subj := fmt.Sprintf("baz.%d", j)
			nc.SubscribeSync(subj)
			nc.SubscribeSync(subj)
		}
		nc.Flush()
	}

	for i := 0; i < totalAccounts; i++ {
		accName := fmt.Sprintf("acc_%d", i)
		checkForRegisteredQSubInterest(t, sa, "B", accName, "bar", 1, 2*time.Second)
	}

	// Resend msgs from A on foo, on all accounts. There will be no interest on this subject.
	produceMsgsFromA(t)

	for i := 0; i < totalAccounts; i++ {
		accName := fmt.Sprintf("acc_%d", i)
		checkForSubjectNoInterest(t, gwc, accName, "foo", true, 2*time.Second)
		// Verify that we still have the queue interest registered
		checkForRegisteredQSubInterest(t, sa, "B", accName, "bar", 1, 2*time.Second)
	}

	// Check accounts...
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		for pollMode := 0; pollMode < 2; pollMode++ {
			g := pollGatewayz(t, sa, pollMode, fmt.Sprintf(gatewayzURL+"?accs=1&accs_limit=%d", totalAccounts+10),
				&GatewayzOptions{Accounts: true, AccountsLimit: totalAccounts + 10})
			og := g.OutboundGateways["B"]
			if og == nil {
				return fmt.Errorf("mode=%v - Expected outbound gateway to B, got none", pollMode)
			}
			if n := len(og.Accounts); n != totalAccounts {
				return fmt.Errorf("mode=%v - Expected to get all %d accounts, got %v", pollMode, totalAccounts, n)
			}
			// Now account details
			for _, acc := range og.Accounts {
				if acc.InterestMode != Optimistic.String() {
					return fmt.Errorf("mode=%v - Expected optimistic mode, got %q", pollMode, acc.InterestMode)
				}
				if acc.NoInterestCount != 1 {
					return fmt.Errorf("mode=%v - Expected 1 no-interest, got %v", pollMode, acc.NoInterestCount)
				}
				if acc.NumQueueSubscriptions != 1 || acc.TotalSubscriptions != 1 {
					return fmt.Errorf("mode=%v - Expected total subs to be 1, got %v - and num queue subs to be 1, got %v",
						pollMode, acc.TotalSubscriptions, acc.NumQueueSubscriptions)
				}
			}
		}
		return nil
	})

	// Check inbound on server B
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		for pollMode := 0; pollMode < 2; pollMode++ {
			// Check that list of accounts is limited by default
			g := pollGatewayz(t, sb, pollMode, gwURLServerB+"?accs=1", &GatewayzOptions{Accounts: true})
			igs := g.InboundGateways["A"]
			if igs == nil {
				return fmt.Errorf("mode=%v - Expected inbound gateway to A, got none", pollMode)
			}
			if len(igs) != 1 {
				return fmt.Errorf("mode=%v - Expected single inbound, got %v", pollMode, len(igs))
			}
			ig := igs[0]
			if n := len(ig.Accounts); n != gwAccountsLimit {
				return fmt.Errorf("mode=%v - Expected to get %d accounts, got %v", pollMode, gwAccountsLimit, n)
			}
			// Ask for specific limit, make sure it works with need for "do accounts" option
			limit := gwAccountsLimit - 3
			g = pollGatewayz(t, sb, pollMode, fmt.Sprintf(gwURLServerB+"?accs_limit=%d", limit),
				&GatewayzOptions{Accounts: true, AccountsLimit: limit})
			igs = g.InboundGateways["A"]
			if igs == nil {
				return fmt.Errorf("mode=%v - Expected inbound gateway to A, got none", pollMode)
			}
			if len(igs) != 1 {
				return fmt.Errorf("mode=%v - Expected single inbound, got %v", pollMode, len(igs))
			}
			ig = igs[0]
			if n := len(ig.Accounts); n != limit {
				return fmt.Errorf("mode=%v - Expected to get %d accounts, got %v", pollMode, limit, n)
			}
			// Ask for more accounts that we know we have so we get them all
			g = pollGatewayz(t, sb, pollMode, fmt.Sprintf(gwURLServerB+"?accs=1&accs_limit=%d", totalAccounts+10),
				&GatewayzOptions{Accounts: true, AccountsLimit: totalAccounts + 10})
			igs = g.InboundGateways["A"]
			if igs == nil {
				return fmt.Errorf("mode=%v - Expected inbound gateway to A, got none", pollMode)
			}
			if len(igs) != 1 {
				return fmt.Errorf("mode=%v - Expected single inbound, got %v", pollMode, len(igs))
			}
			ig = igs[0]
			if ig.Connection == nil {
				return fmt.Errorf("mode=%v - Expected inbound connection from A to be set, wat not", pollMode)
			}
			if ig.Connection.Name != sa.ID() {
				t.Fatalf("mode=%v - Expected inbound connection from A to have name %q, got %q", pollMode, sa.ID(), ig.Connection.Name)
			}
			if n := len(ig.Accounts); n != totalAccounts {
				return fmt.Errorf("mode=%v - Expected to get all %d accounts, got %v", pollMode, totalAccounts, n)
			}
			// Now account details
			for _, acc := range ig.Accounts {
				if acc.InterestMode != Optimistic.String() {
					return fmt.Errorf("mode=%v - Expected optimistic mode, got %q", pollMode, acc.InterestMode)
				}
				if acc.NoInterestCount != 1 {
					return fmt.Errorf("mode=%v - Expected 1 no-interest, got %v", pollMode, acc.NoInterestCount)
				}
				// For inbound gateway, NumQueueSubscriptions and TotalSubscriptions
				// are not relevant.
				if acc.NumQueueSubscriptions != 0 || acc.TotalSubscriptions != 0 {
					return fmt.Errorf("mode=%v - For inbound connection, expected num queue subs and total subs to be 0, got %v and %v",
						pollMode, acc.TotalSubscriptions, acc.NumQueueSubscriptions)
				}
			}
		}
		return nil
	})

	// Make one of the account to switch to interest only
	nc, err := nats.Connect(fmt.Sprintf("nats://user_1:pwd@%s:%d", oa.Host, oa.Port))
	if err != nil {
		t.Fatalf("Error on connect: %v", err)
	}
	defer nc.Close()
	for i := 0; i < 1100; i++ {
		nc.Publish(fmt.Sprintf("foo.%d", i), []byte("hello"))
	}
	nc.Flush()
	nc.Close()

	// Check that we can select single account
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		for pollMode := 0; pollMode < 2; pollMode++ {
			g := pollGatewayz(t, sa, pollMode, gatewayzURL+"?gw_name=B&acc_name=acc_1", &GatewayzOptions{Name: "B", AccountName: "acc_1"})
			og := g.OutboundGateways["B"]
			if og == nil {
				return fmt.Errorf("mode=%v - Expected outbound gateway to B, got none", pollMode)
			}
			if n := len(og.Accounts); n != 1 {
				return fmt.Errorf("mode=%v - Expected to get 1 account, got %v", pollMode, n)
			}
			// Now account details
			acc := og.Accounts[0]
			if acc.InterestMode != InterestOnly.String() {
				return fmt.Errorf("mode=%v - Expected interest-only mode, got %q", pollMode, acc.InterestMode)
			}
			// Since we switched, this should be set to 0
			if acc.NoInterestCount != 0 {
				return fmt.Errorf("mode=%v - Expected 0 no-interest, got %v", pollMode, acc.NoInterestCount)
			}
			// We have created 3 subs on that account on B, and 1 queue sub.
			// So total should be 4 and 1 for queue sub.
			if acc.NumQueueSubscriptions != 1 {
				return fmt.Errorf("mode=%v - Expected num queue subs to be 1, got %v",
					pollMode, acc.NumQueueSubscriptions)
			}
			if acc.TotalSubscriptions != 4 {
				return fmt.Errorf("mode=%v - Expected total subs to be 4, got %v",
					pollMode, acc.TotalSubscriptions)
			}
		}
		return nil
	})

	// Check inbound on B now...
	checkFor(t, 2*time.Second, 15*time.Millisecond, func() error {
		for pollMode := 0; pollMode < 2; pollMode++ {
			g := pollGatewayz(t, sb, pollMode, gwURLServerB+"?gw_name=A&acc_name=acc_1", &GatewayzOptions{Name: "A", AccountName: "acc_1"})
			igs := g.InboundGateways["A"]
			if igs == nil {
				return fmt.Errorf("mode=%v - Expected inbound gateway from A, got none", pollMode)
			}
			if len(igs) != 1 {
				return fmt.Errorf("mode=%v - Expected single inbound, got %v", pollMode, len(igs))
			}
			ig := igs[0]
			if n := len(ig.Accounts); n != 1 {
				return fmt.Errorf("mode=%v - Expected to get 1 account, got %v", pollMode, n)
			}
			// Now account details
			acc := ig.Accounts[0]
			if acc.InterestMode != InterestOnly.String() {
				return fmt.Errorf("mode=%v - Expected interest-only mode, got %q", pollMode, acc.InterestMode)
			}
			if acc.InterestMode != InterestOnly.String() {
				return fmt.Errorf("Should be in %q mode, got %q", InterestOnly.String(), acc.InterestMode)
			}
			// Since we switched, this should be set to 0
			if acc.NoInterestCount != 0 {
				return fmt.Errorf("mode=%v - Expected 0 no-interest, got %v", pollMode, acc.NoInterestCount)
			}
			// Again, for inbound, these should be always 0.
			if acc.NumQueueSubscriptions != 0 || acc.TotalSubscriptions != 0 {
				return fmt.Errorf("mode=%v - For inbound connection, expected num queue subs and total subs to be 0, got %v and %v",
					pollMode, acc.TotalSubscriptions, acc.NumQueueSubscriptions)
			}
		}
		return nil
	})
}
