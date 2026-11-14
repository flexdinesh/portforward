package main

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestTTYReaderReads(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tty := &ttyReader{fd: int(reader.Fd())}
	if _, err := writer.WriteString("j\r"); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 8)
	if n, err := tty.Read(buffer); err != nil || string(buffer[:n]) != "j\r" {
		t.Fatalf("read %q, %v", buffer[:n], err)
	}
	writer.Close()
	if _, err := tty.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v", err)
	}
}

// After stop returns, no read is pending, so later input reaches its next reader.
func TestTTYReaderStopLeavesInputUnread(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	tty := &ttyReader{fd: int(reader.Fd())}
	result := make(chan error, 1)
	go func() {
		_, err := tty.Read(make([]byte, 8))
		result <- err
	}()
	time.Sleep(2 * ttyWait)
	tty.stop()
	select {
	case err := <-result:
		if !errors.Is(err, errInputStopped) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read did not stop")
	}
	if _, err := writer.WriteString("secret\n"); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 16)
	if n, err := reader.Read(buffer); err != nil || string(buffer[:n]) != "secret\n" {
		t.Fatalf("next reader got %q, %v", buffer[:n], err)
	}
}

// Input arriving while a stopping read waits stays for the next reader.
func TestTTYReaderStoppingLeavesNewInputUnread(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	tty := &ttyReader{fd: int(reader.Fd())}
	result := make(chan error, 1)
	go func() {
		_, err := tty.Read(make([]byte, 8))
		result <- err
	}()
	time.Sleep(ttyWait / 2)
	tty.stopped.Store(true)
	if _, err := writer.WriteString("5432"); err != nil {
		t.Fatal(err)
	}
	tty.stop()
	if err := <-result; !errors.Is(err, errInputStopped) {
		t.Fatalf("err = %v", err)
	}
	buffer := make([]byte, 16)
	if n, err := reader.Read(buffer); err != nil || string(buffer[:n]) != "5432" {
		t.Fatalf("next reader got %q, %v", buffer[:n], err)
	}
}
