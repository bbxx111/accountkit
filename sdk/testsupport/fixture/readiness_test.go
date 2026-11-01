package main

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestReadinessProbeCancelsUnresponsiveListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- probeReady(ctx, "http://"+ln.Addr().String()) }()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("probe did not connect")
	}
	defer conn.Close()
	cancel()
	select {
	case ready := <-done:
		if ready {
			t.Fatal("unresponsive listener reported ready")
		}
	case <-time.After(500 * time.Millisecond):
		conn.Close()
		<-done
		t.Fatal("readiness probe ignored context cancellation")
	}
}

func TestReadinessProbeBoundsUnresponsiveListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	done := make(chan bool, 1)
	go func() { done <- probeReady(context.Background(), "http://"+ln.Addr().String()) }()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("probe did not connect")
	}
	defer conn.Close()
	select {
	case ready := <-done:
		if ready {
			t.Fatal("unresponsive listener reported ready")
		}
	case <-time.After(2 * time.Second):
		conn.Close()
		<-done
		t.Fatal("readiness probe exceeded its request budget")
	}
}
