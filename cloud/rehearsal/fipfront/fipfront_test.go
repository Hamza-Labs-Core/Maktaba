package fipfront

import (
	"bufio"
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// Each connection is looked up and piped both ways.
func TestServePipes(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = echo.Close() }()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				line, _ := bufio.NewReader(c).ReadString('\n')
				_, _ = c.Write([]byte("echo " + line))
				_ = c.Close()
			}()
		}
	}()
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = front.Close() }()
	go func() { _ = Serve(front, echo.Addr().String()) }()
	for range 3 {
		c, err := net.Dial("tcp", front.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = c.Write([]byte("hi\n"))
		got, _ := bufio.NewReader(c).ReadString('\n')
		_ = c.Close()
		if got != "echo hi\n" {
			t.Errorf("got %q", got)
		}
	}
}

// Follow: a connection made before the name moves is closed once it answers another address;
// one made after reaches the new holder.
func TestFollowClosesConnectionsToTheOldHolder(t *testing.T) {
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	go func() {
		for {
			c, err := holder.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = bufio.NewReader(c).ReadString('\n'); _ = c.Close() }() // holds it open
		}
	}()
	var moved atomic.Bool
	resolve := func(_ context.Context, _ string) ([]string, error) {
		if moved.Load() {
			return []string{"127.0.0.2"}, nil
		}
		return []string{"127.0.0.1"}, nil
	}
	_, port, _ := net.SplitHostPort(holder.Addr().String())
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = front.Close() }()
	go func() { _ = Follow(front, net.JoinHostPort("127.0.0.1", port), 20*time.Millisecond, resolve) }()
	c, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	time.Sleep(100 * time.Millisecond)
	_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, err := c.Read(make([]byte, 1)); err == nil || !isTimeout(err) {
		t.Fatalf("before the move the connection should stay open, got %v", err)
	}
	moved.Store(true)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("after the move the old holder's connection should be closed, got %v", err)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
