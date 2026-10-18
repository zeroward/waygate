package clamav

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseScanReply(t *testing.T) {
	if err := parseScanReply("stream: OK"); err != nil {
		t.Fatal(err)
	}
	err := parseScanReply("stream: Win.Test.EICAR_HDB-1 FOUND")
	var inf *InfectedError
	if !errors.As(err, &inf) || inf.Signature != "Win.Test.EICAR_HDB-1" {
		t.Fatalf("got %v", err)
	}
	if !errors.Is(err, ErrInfected) {
		t.Fatal("expected ErrInfected")
	}
	if err := parseScanReply("INSTREAM size limit exceeded. ERROR"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestINSTREAM(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handle := func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			cmd := make([]byte, 10)
			if _, err := io.ReadFull(conn, cmd); err != nil {
				return
			}
			if string(cmd) != "nINSTREAM\n" {
				_, _ = conn.Write([]byte("ERROR\n"))
				return
			}
			var payload bytes.Buffer
			hdr := make([]byte, 4)
			for {
				if _, err := io.ReadFull(conn, hdr); err != nil {
					return
				}
				n := binary.BigEndian.Uint32(hdr)
				if n == 0 {
					break
				}
				chunk := make([]byte, n)
				if _, err := io.ReadFull(conn, chunk); err != nil {
					return
				}
				payload.Write(chunk)
			}
			if strings.Contains(payload.String(), "EICAR-STANDARD-ANTIVIRUS-TEST-FILE") {
				_, _ = conn.Write([]byte("stream: Eicar-Test-Signature FOUND\n"))
				return
			}
			_, _ = conn.Write([]byte("stream: OK\n"))
		}
		handle()
		handle()
	}()

	c := New(ln.Addr().String(), 5*time.Second)
	if err := c.Scan(context.Background(), strings.NewReader("clean zip bytes")); err != nil {
		t.Fatalf("clean: %v", err)
	}
	err = c.Scan(context.Background(), strings.NewReader("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*"))
	if !errors.Is(err, ErrInfected) {
		t.Fatalf("infected: %v", err)
	}
	<-done
}

func TestUnavailable(t *testing.T) {
	c := New("127.0.0.1:1", time.Second)
	if err := c.Ping(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}
