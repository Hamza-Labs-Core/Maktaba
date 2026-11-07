package fipfront

import (
	"bufio"
	"net"
	"testing"
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
