//go:build windows

package discovery

// DnsServiceRegister (dnsapi.dll, Windows 10 1809+): the DNS Client service's mDNS
// responder, which already answers for this machine's name, publishes the service. Where
// dnsapi.dll has no DnsServiceRegister (an older Windows), zeroconf announces instead.
// The same file is in api/internal/discovery and cmd/maktaba-server/internal/mdns (two
// modules); tools/ci/mdns_copies.py checks they stay the same.

import (
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	dnsapiDLL        = windows.NewLazySystemDLL("dnsapi.dll")
	procRegister     = dnsapiDLL.NewProc("DnsServiceRegister")
	procDeRegister   = dnsapiDLL.NewProc("DnsServiceDeRegister")
	procConstruct    = dnsapiDLL.NewProc("DnsServiceConstructInstance")
	procFreeInstance = dnsapiDLL.NewProc("DnsServiceFreeInstance")
	errNoInstance    = errors.New("DnsServiceConstructInstance failed")

	winRegs    sync.Map // pQueryContext → *winReg
	winRegNext atomic.Uintptr

	// one callback for every registration (a process can make only so many)
	registerCallback     uintptr
	registerCallbackOnce sync.Once
)

const (
	dnsQueryRequestVersion1 = 1
	dnsRequestPending       = 9506 // DNS_REQUEST_PENDING: the request is taken
)

// dnsapiAvailable is whether this Windows has the DNS-SD registration API.
func dnsapiAvailable() bool {
	for _, p := range []*windows.LazyProc{procRegister, procDeRegister, procConstruct, procFreeInstance} {
		if p.Find() != nil {
			return false
		}
	}
	return true
}

// systemDNSAPIImpl is the real dnsapi.dll.
func systemDNSAPIImpl() dnsAPI { return windowsDNSAPI{} }

// dnsServiceRegisterRequest is DNS_SERVICE_REGISTER_REQUEST.
type dnsServiceRegisterRequest struct {
	Version         uint32
	InterfaceIndex  uint32
	ServiceInstance uintptr // PDNS_SERVICE_INSTANCE, dnsapi's memory
	Callback        uintptr // PDNS_SERVICE_REGISTER_COMPLETE
	QueryContext    uintptr
	Credentials     uintptr
	UnicastEnabled  int32
}

type windowsDNSAPI struct{}

// winReg is one registration: the request Windows holds (pinned until it lets go), the
// instance dnsapi built, and who waits for the outcome.
type winReg struct {
	id   uintptr
	req  *dnsServiceRegisterRequest
	pin  runtime.Pinner
	mu   sync.Mutex
	done func(error)
	gone chan struct{} // closed once Windows let go of it
	// state: registering until Windows answers; then registered, or gone once refused or
	// withdrawn (and its memory let go)
	state int
}

const (
	winRegistering = iota
	winRegistered
	winWithdrawLater // withdrawn while registering: withdrawn once Windows answers
	winDeregistering
	winGone
)

func (windowsDNSAPI) register(svc winService, done func(error)) (winRegistration, error) {
	inst, err := constructInstance(svc)
	if err != nil {
		return nil, err
	}
	registerCallbackOnce.Do(func() { registerCallback = windows.NewCallback(onRegisterComplete) })
	r := &winReg{id: winRegNext.Add(1), done: done, gone: make(chan struct{})}
	r.req = &dnsServiceRegisterRequest{
		Version:         dnsQueryRequestVersion1,
		ServiceInstance: inst,
		Callback:        registerCallback,
		QueryContext:    r.id,
	}
	r.pin.Pin(r.req)
	winRegs.Store(r.id, r)
	ret, _, _ := procRegister.Call(uintptr(unsafe.Pointer(r.req)), 0)
	if ret != dnsRequestPending {
		r.release()
		return nil, syscall.Errno(ret)
	}
	return r, nil
}

// deregister implements winRegistration: Windows withdraws it, and answers through the
// same callback once it has. It waits a moment for that, so the same name can be
// registered again at once without a conflict with itself.
func (r *winReg) deregister() {
	r.mu.Lock()
	was := r.state
	switch was {
	case winRegistering:
		r.state = winWithdrawLater
	case winRegistered:
		r.state = winDeregistering
	}
	r.mu.Unlock()
	if was == winRegistered {
		r.withdraw()
	}
	select {
	case <-r.gone:
	case <-time.After(2 * time.Second):
	}
}

// withdraw asks Windows to withdraw r (its state winDeregistering).
func (r *winReg) withdraw() {
	ret, _, _ := procDeRegister.Call(uintptr(unsafe.Pointer(r.req)), 0)
	if ret != dnsRequestPending {
		r.release() // nothing to withdraw: Windows holds nothing of it
	}
}

// release lets go of r's memory once Windows does.
func (r *winReg) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == winGone {
		return
	}
	r.state = winGone
	winRegs.Delete(r.id)
	if r.req.ServiceInstance != 0 {
		_, _, _ = procFreeInstance.Call(r.req.ServiceInstance)
	}
	r.pin.Unpin()
	close(r.gone)
}

// onRegisterComplete is DNS_SERVICE_REGISTER_COMPLETE, for registering and for
// withdrawing.
func onRegisterComplete(status, ctx, inst uintptr) uintptr {
	v, ok := winRegs.Load(ctx)
	if !ok {
		return 0
	}
	r := v.(*winReg)
	if inst != 0 && inst != r.req.ServiceInstance {
		_, _, _ = procFreeInstance.Call(inst) // the copy the answer carries is ours to free
	}
	r.mu.Lock()
	state := r.state
	switch {
	case state == winRegistering && status == 0:
		r.state = winRegistered
	case state == winWithdrawLater && status == 0:
		r.state = winDeregistering
	}
	r.mu.Unlock()
	switch {
	case state == winRegistering && status == 0:
		r.done(nil)
	case state == winRegistering:
		r.release()
		r.done(syscall.Errno(status))
	case state == winWithdrawLater && status == 0:
		go r.withdraw() // not from inside Windows' own callback
	case state == winWithdrawLater, state == winDeregistering:
		r.release()
	}
	return 0
}

// constructInstance is DnsServiceConstructInstance for svc (dnsapi's memory, freed with
// DnsServiceFreeInstance).
func constructInstance(svc winService) (uintptr, error) {
	name, err := windows.UTF16PtrFromString(svc.instance)
	if err != nil {
		return 0, err
	}
	host, err := windows.UTF16PtrFromString(svc.host)
	if err != nil {
		return 0, err
	}
	var ip4 *uint32
	if ip := net.ParseIP(svc.ip4).To4(); ip != nil {
		v := *(*uint32)(unsafe.Pointer(&ip[0])) // IP4_ADDRESS: the bytes in network order
		ip4 = &v
	}
	var ip6 *[16]byte
	if ip := net.ParseIP(svc.ip6); ip != nil && ip.To4() == nil {
		var v [16]byte
		copy(v[:], ip.To16())
		ip6 = &v
	}
	keys := make([]*uint16, len(svc.keys))
	values := make([]*uint16, len(svc.values))
	for i := range svc.keys {
		if keys[i], err = windows.UTF16PtrFromString(svc.keys[i]); err != nil {
			return 0, err
		}
		if values[i], err = windows.UTF16PtrFromString(svc.values[i]); err != nil {
			return 0, err
		}
	}
	var keysPtr, valuesPtr uintptr
	if len(keys) > 0 {
		keysPtr, valuesPtr = uintptr(unsafe.Pointer(&keys[0])), uintptr(unsafe.Pointer(&values[0]))
	}
	inst, _, _ := procConstruct.Call(
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(host)),
		uintptr(unsafe.Pointer(ip4)), uintptr(unsafe.Pointer(ip6)),
		uintptr(uint16(svc.port)), 0, 0,
		uintptr(len(keys)), keysPtr, valuesPtr,
	)
	runtime.KeepAlive(name)
	runtime.KeepAlive(host)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(values)
	runtime.KeepAlive(ip4)
	runtime.KeepAlive(ip6)
	if inst == 0 {
		return 0, errNoInstance
	}
	return inst, nil
}
