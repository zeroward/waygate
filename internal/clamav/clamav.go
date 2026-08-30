// Package clamav talks to clamd over the INSTREAM TCP protocol.
package clamav

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

var (
	ErrUnavailable = errors.New("virus scanner unavailable")
	ErrInfected    = errors.New("file failed the virus scan")
)

type InfectedError struct {
	Signature string
}

func (e *InfectedError) Error() string {
	if e == nil || e.Signature == "" {
		return ErrInfected.Error()
	}
	return "file failed the virus scan (" + e.Signature + ")"
}

func (e *InfectedError) Unwrap() error { return ErrInfected }

type Client struct {
	Addr    string
	Timeout time.Duration
	Dialer  *net.Dialer
}

func New(addr string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &Client{
		Addr:    strings.TrimSpace(addr),
		Timeout: timeout,
		Dialer:  &net.Dialer{Timeout: 8 * time.Second},
	}
}

func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.command(ctx, "nPING\n", nil)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(resp)), "PONG") {
		return fmt.Errorf("%w: unexpected ping reply %q", ErrUnavailable, resp)
	}
	return nil
}

func (c *Client) Scan(ctx context.Context, r io.Reader) error {
	if c == nil || c.Addr == "" {
		return ErrUnavailable
	}
	resp, err := c.command(ctx, "nINSTREAM\n", r)
	if err != nil {
		return err
	}
	return parseScanReply(resp)
}

func parseScanReply(resp string) error {
	line := strings.TrimSpace(resp)
	upper := strings.ToUpper(line)
	switch {
	case strings.HasSuffix(upper, " OK"):
		return nil
	case strings.Contains(upper, " FOUND"):
		sig := strings.TrimSpace(line)
		if i := strings.LastIndex(sig, ":"); i >= 0 {
			sig = strings.TrimSpace(sig[i+1:])
		}
		sig = strings.TrimSuffix(sig, " FOUND")
		sig = strings.TrimSuffix(sig, " found")
		return &InfectedError{Signature: strings.TrimSpace(sig)}
	case strings.Contains(upper, "ERROR"):
		return fmt.Errorf("%w: %s", ErrUnavailable, line)
	default:
		return fmt.Errorf("%w: %s", ErrUnavailable, line)
	}
}

func (c *Client) command(ctx context.Context, header string, body io.Reader) (string, error) {
	if c == nil || c.Addr == "" {
		return "", ErrUnavailable
	}
	dialCtx := ctx
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	conn, err := c.Dialer.DialContext(dialCtx, "tcp", c.Addr)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()

	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if _, err := io.WriteString(conn, header); err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if body != nil {
		buf := make([]byte, 256*1024)
		var nbuf [4]byte
		for {
			n, readErr := body.Read(buf)
			if n > 0 {
				binary.BigEndian.PutUint32(nbuf[:], uint32(n))
				if _, err := conn.Write(nbuf[:]); err != nil {
					return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
				}
				if _, err := conn.Write(buf[:n]); err != nil {
					return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return "", readErr
			}
		}
		binary.BigEndian.PutUint32(nbuf[:], 0)
		if _, err := conn.Write(nbuf[:]); err != nil {
			return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
	}

	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return strings.TrimRight(reply, "\r\n"), nil
}
