package main

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/chrisgane/dayz_queueWaiter/internal/detect"
)

// Screen capture. Fullscreen games often show up black to the simple
// (GDI) way of reading the screen, so this uses DXGI Desktop Duplication,
// which is what OBS's display capture uses, and falls back to GDI only if
// that isn't available.

type capturer interface {
	// Grab returns the latest picture of the monitor. The frame is reused
	// by the next Grab.
	Grab() (*detect.BGRA, error)
	Close()
	Method() string
}

// openCapture captures the monitor containing the point (x, y).
func openCapture(x, y int32) (capturer, error) {
	d, err := openDuplication(x, y)
	if err == nil {
		return d, nil
	}
	g, gerr := openGDI(x, y)
	if gerr != nil {
		return nil, fmt.Errorf("screen capture: %v; GDI: %v", err, gerr)
	}
	g.why = err
	return g, nil
}

// --- DXGI Desktop Duplication ---

var (
	dxgiDLL               = windows.NewLazySystemDLL("dxgi.dll")
	d3d11DLL              = windows.NewLazySystemDLL("d3d11.dll")
	pCreateDXGIFactory1   = dxgiDLL.NewProc("CreateDXGIFactory1")
	pD3D11CreateDevice    = d3d11DLL.NewProc("D3D11CreateDevice")
	iidIDXGIFactory1      = windows.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba, Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
	iidIDXGIOutput1       = windows.GUID{Data1: 0x00cddea8, Data2: 0x939b, Data3: 0x4b83, Data4: [8]byte{0xa3, 0x40, 0xa6, 0x85, 0x22, 0x66, 0x66, 0xcc}}
	iidID3D11Texture2D    = windows.GUID{Data1: 0x6f15aaf2, Data2: 0xd208, Data3: 0x4e89, Data4: [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}}
	errNoFrame            = errors.New("no picture from the screen yet")
	errDuplicationTimeout = uintptr(0x887A0027) // DXGI_ERROR_WAIT_TIMEOUT
	errAccessLost         = uintptr(0x887A0026) // DXGI_ERROR_ACCESS_LOST
	errNotFound           = uintptr(0x887A0002) // DXGI_ERROR_NOT_FOUND
)

// Vtable slots of the COM methods used.
const (
	vRelease                = 2
	vQueryInterface         = 0
	vFactoryEnumAdapters    = 7
	vAdapterEnumOutputs     = 7
	vOutputGetDesc          = 7
	vOutput1DuplicateOutput = 22
	vDupAcquireNextFrame    = 8
	vDupReleaseFrame        = 14
	vDeviceCreateTexture2D  = 5
	vTextureGetDesc         = 10
	vContextMap             = 14
	vContextUnmap           = 15
	vContextCopyResource    = 47
)

// com is a COM interface pointer.
type com struct{ vtbl *[64]uintptr }

//go:uintptrescapes
func (c *com) call(slot int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(c.vtbl[slot], append([]uintptr{uintptr(unsafe.Pointer(c))}, args...)...)
	return r
}

func (c *com) release() {
	if c != nil {
		c.call(vRelease)
	}
}

func (c *com) query(iid *windows.GUID) (*com, error) {
	var out *com
	if hr := c.call(vQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); failed(hr) {
		return nil, hresult("QueryInterface", hr)
	}
	return out, nil
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

func hresult(what string, hr uintptr) error {
	return fmt.Errorf("%s failed (0x%08X)", what, uint32(hr))
}

type outputDesc struct {
	DeviceName               [32]uint16
	Left, Top, Right, Bottom int32
	AttachedToDesktop        int32
	Rotation                 uint32
	Monitor                  uintptr
}

type texture2DDesc struct {
	Width, Height  uint32
	MipLevels      uint32
	ArraySize      uint32
	Format         uint32
	SampleCount    uint32
	SampleQuality  uint32
	Usage          uint32
	BindFlags      uint32
	CPUAccessFlags uint32
	MiscFlags      uint32
}

type frameInfo struct {
	LastPresentTime         int64
	LastMouseUpdateTime     int64
	AccumulatedFrames       uint32
	RectsCoalesced          int32
	ProtectedContentMasked  int32
	PointerX, PointerY      int32
	PointerVisible          int32
	TotalMetadataBufferSize uint32
	PointerShapeBufferSize  uint32
}

type mappedSubresource struct {
	Data       unsafe.Pointer
	RowPitch   uint32
	DepthPitch uint32
}

type duplication struct {
	adapter, output  *com
	device, ctx, dup *com
	staging          *com
	frame            detect.BGRA
	haveFrame        bool
}

func openDuplication(x, y int32) (*duplication, error) {
	var factory *com
	if r, _, _ := pCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&iidIDXGIFactory1)), uintptr(unsafe.Pointer(&factory))); failed(r) {
		return nil, hresult("CreateDXGIFactory1", r)
	}
	defer factory.release()

	d := &duplication{}
	// Find the monitor (output) containing the point, and its graphics card.
	for a := uintptr(0); d.output == nil; a++ {
		var adapter *com
		hr := factory.call(vFactoryEnumAdapters, a, uintptr(unsafe.Pointer(&adapter)))
		if hr == errNotFound {
			break
		}
		if failed(hr) {
			return nil, hresult("EnumAdapters", hr)
		}
		for o := uintptr(0); ; o++ {
			var output *com
			hr := adapter.call(vAdapterEnumOutputs, o, uintptr(unsafe.Pointer(&output)))
			if failed(hr) {
				break
			}
			var desc outputDesc
			output.call(vOutputGetDesc, uintptr(unsafe.Pointer(&desc)))
			if x >= desc.Left && x < desc.Right && y >= desc.Top && y < desc.Bottom {
				d.adapter, d.output = adapter, output
				d.frame.W, d.frame.H = int(desc.Right-desc.Left), int(desc.Bottom-desc.Top)
				break
			}
			output.release()
		}
		if d.output == nil {
			adapter.release()
		}
	}
	if d.output == nil {
		return nil, errors.New("couldn't find the monitor under the mouse")
	}
	if err := d.start(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *duplication) start() error {
	const driverTypeUnknown, bgraSupport, sdkVersion = 0, 0x20, 7
	var featureLevel uint32
	if r, _, _ := pD3D11CreateDevice.Call(uintptr(unsafe.Pointer(d.adapter)), driverTypeUnknown, 0, bgraSupport, 0, 0, sdkVersion,
		uintptr(unsafe.Pointer(&d.device)), uintptr(unsafe.Pointer(&featureLevel)), uintptr(unsafe.Pointer(&d.ctx))); failed(r) {
		return hresult("D3D11CreateDevice", r)
	}
	output1, err := d.output.query(&iidIDXGIOutput1)
	if err != nil {
		return err
	}
	defer output1.release()
	if hr := output1.call(vOutput1DuplicateOutput, uintptr(unsafe.Pointer(d.device)), uintptr(unsafe.Pointer(&d.dup))); failed(hr) {
		return hresult("DuplicateOutput", hr)
	}
	return nil
}

// restart sets duplication up again, after a resolution change or the game
// switching display mode.
func (d *duplication) restart() error {
	d.stop()
	return d.start()
}

func (d *duplication) stop() {
	for _, c := range []**com{&d.staging, &d.dup, &d.ctx, &d.device} {
		(*c).release()
		*c = nil
	}
}

func (d *duplication) Close() {
	d.stop()
	d.output.release()
	d.adapter.release()
}

func (d *duplication) Method() string { return "DXGI desktop duplication" }

func (d *duplication) Grab() (*detect.BGRA, error) {
	lastErr := errNoFrame
	for range 20 {
		if d.dup == nil {
			// Duplication can be refused for a moment while the game
			// switches display mode.
			if err := d.restart(); err != nil {
				d.stop()
				lastErr = err
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}
		var info frameInfo
		var resource *com
		hr := d.dup.call(vDupAcquireNextFrame, 50, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&resource)))
		switch {
		case hr == errDuplicationTimeout:
			// Nothing has changed on screen.
			if d.haveFrame {
				return &d.frame, nil
			}
			continue
		case hr == errAccessLost:
			d.stop()
			continue
		case failed(hr):
			return nil, hresult("AcquireNextFrame", hr)
		}
		err := d.copyFrame(&info, resource)
		resource.release()
		d.dup.call(vDupReleaseFrame)
		if err != nil {
			return nil, err
		}
		if d.haveFrame {
			return &d.frame, nil
		}
	}
	return nil, lastErr
}

func (d *duplication) copyFrame(info *frameInfo, resource *com) error {
	if info.AccumulatedFrames == 0 && d.haveFrame {
		return nil // only the mouse moved
	}
	tex, err := resource.query(&iidID3D11Texture2D)
	if err != nil {
		return err
	}
	defer tex.release()
	if d.staging == nil {
		var desc texture2DDesc
		tex.call(vTextureGetDesc, uintptr(unsafe.Pointer(&desc)))
		const usageStaging, cpuAccessRead = 3, 0x20000
		desc.MipLevels, desc.ArraySize = 1, 1
		desc.SampleCount, desc.SampleQuality = 1, 0
		desc.Usage, desc.BindFlags, desc.CPUAccessFlags, desc.MiscFlags = usageStaging, 0, cpuAccessRead, 0
		if hr := d.device.call(vDeviceCreateTexture2D, uintptr(unsafe.Pointer(&desc)), 0, uintptr(unsafe.Pointer(&d.staging))); failed(hr) {
			return hresult("CreateTexture2D", hr)
		}
		d.frame.W, d.frame.H = int(desc.Width), int(desc.Height)
		d.frame.Stride = d.frame.W * 4
		d.frame.Pix = make([]byte, d.frame.Stride*d.frame.H)
	}
	d.ctx.call(vContextCopyResource, uintptr(unsafe.Pointer(d.staging)), uintptr(unsafe.Pointer(tex)))
	var m mappedSubresource
	const mapRead = 1
	if hr := d.ctx.call(vContextMap, uintptr(unsafe.Pointer(d.staging)), 0, mapRead, 0, uintptr(unsafe.Pointer(&m))); failed(hr) {
		return hresult("Map", hr)
	}
	row := d.frame.Stride
	for y := 0; y < d.frame.H; y++ {
		src := unsafe.Slice((*byte)(unsafe.Add(m.Data, y*int(m.RowPitch))), row)
		copy(d.frame.Pix[y*row:(y+1)*row], src)
	}
	d.ctx.call(vContextUnmap, uintptr(unsafe.Pointer(d.staging)), 0)
	d.haveFrame = true
	return nil
}

// --- GDI ---

var (
	pGetDC              = user32.NewProc("GetDC")
	pReleaseDC          = user32.NewProc("ReleaseDC")
	pMonitorFromPoint   = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW    = user32.NewProc("GetMonitorInfoW")
	gdi32               = windows.NewLazySystemDLL("gdi32.dll")
	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pBitBlt             = gdi32.NewProc("BitBlt")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
)

type rect struct{ Left, Top, Right, Bottom int32 }

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type gdiCapture struct {
	left, top   int32
	screen, mem uintptr
	bitmap, old uintptr
	frame       detect.BGRA
	why         error // why DXGI wasn't used
}

func monitorAt(x, y int32) (rect, error) {
	const defaultToNearest = 2
	mon, _, _ := pMonitorFromPoint.Call(uintptr(uint32(x))|uintptr(uint32(y))<<32, defaultToNearest)
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if r, _, _ := pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return rect{}, errors.New("GetMonitorInfo failed")
	}
	return mi.Monitor, nil
}

func openGDI(x, y int32) (*gdiCapture, error) {
	m, err := monitorAt(x, y)
	if err != nil {
		return nil, err
	}
	g := &gdiCapture{left: m.Left, top: m.Top}
	g.frame.W, g.frame.H = int(m.Right-m.Left), int(m.Bottom-m.Top)
	g.frame.Stride = g.frame.W * 4
	g.screen, _, _ = pGetDC.Call(0)
	g.mem, _, _ = pCreateCompatibleDC.Call(g.screen)
	bi := bitmapInfoHeader{Width: int32(g.frame.W), Height: -int32(g.frame.H), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	g.bitmap, _, _ = pCreateDIBSection.Call(g.mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if g.bitmap == 0 || bits == nil {
		g.Close()
		return nil, errors.New("CreateDIBSection failed")
	}
	g.old, _, _ = pSelectObject.Call(g.mem, g.bitmap)
	g.frame.Pix = unsafe.Slice((*byte)(bits), g.frame.Stride*g.frame.H)
	return g, nil
}

func (g *gdiCapture) Grab() (*detect.BGRA, error) {
	const srcCopy, captureBlt = 0x00CC0020, 0x40000000
	r, _, _ := pBitBlt.Call(g.mem, 0, 0, uintptr(g.frame.W), uintptr(g.frame.H), g.screen, uintptr(g.left), uintptr(g.top), srcCopy|captureBlt)
	if r == 0 {
		return nil, errors.New("BitBlt failed")
	}
	return &g.frame, nil
}

func (g *gdiCapture) Close() {
	if g.old != 0 {
		pSelectObject.Call(g.mem, g.old)
	}
	if g.bitmap != 0 {
		pDeleteObject.Call(g.bitmap)
	}
	if g.mem != 0 {
		pDeleteDC.Call(g.mem)
	}
	if g.screen != 0 {
		pReleaseDC.Call(0, g.screen)
	}
}

func (g *gdiCapture) Method() string { return fmt.Sprintf("GDI (DXGI unavailable: %v)", g.why) }
