package app

import (
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"sync"
)

// PprofOptions controls the opt-in localhost diagnostics server.
type PprofOptions struct {
	Enabled bool
	Addr    string
	Mutex   bool
	Block   bool
}

var pprofState struct {
	sync.Mutex
	server *http.Server
	ln     net.Listener
	addr   string
}

func startPprof(o PprofOptions) error {
	pprofState.Lock()
	defer pprofState.Unlock()
	if pprofState.server != nil {
		_ = pprofState.server.Close()
		pprofState.server = nil
		pprofState.ln = nil
		pprofState.addr = ""
	}
	if !o.Enabled {
		runtime.SetMutexProfileFraction(0)
		runtime.SetBlockProfileRate(0)
		return nil
	}
	addr := o.Addr
	if addr == "" {
		addr = "127.0.0.1:6060"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("pprof address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("pprof diagnostics must bind to localhost, got %q", host)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if o.Mutex {
		runtime.SetMutexProfileFraction(1)
	}
	if o.Block {
		runtime.SetBlockProfileRate(1)
	}
	srv := &http.Server{}
	pprofState.server = srv
	pprofState.ln = ln
	pprofState.addr = ln.Addr().String()
	go func() { _ = srv.Serve(ln) }()
	return nil
}

func activePprofAddr() string {
	pprofState.Lock()
	defer pprofState.Unlock()
	return pprofState.addr
}
