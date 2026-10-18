package detect

import (
	"fmt"
	"image"
	_ "image/jpeg"
	"os"
	"testing"
)

// imageFrame adapts an image, resized to w×h by nearest neighbour, to Frame.
type imageFrame struct {
	img  image.Image
	w, h int
}

func (f imageFrame) Size() (int, int) { return f.w, f.h }

func (f imageFrame) RGB(x, y int) (uint8, uint8, uint8) {
	b := f.img.Bounds()
	sx := b.Min.X + x*b.Dx()/f.w
	sy := b.Min.Y + y*b.Dy()/f.h
	r, g, bl, _ := f.img.At(sx, sy).RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)
}

func load(t *testing.T, name string) image.Image {
	t.Helper()
	file, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// The test screenshots are real DayZ screens at 2000×1125.
func TestClassify(t *testing.T) {
	cases := []struct {
		file string
		want State
	}{
		{"browser.jpg", Browser},
		{"full_popup.jpg", PopupFull},
		{"loading.jpg", Away},
	}
	sizes := [][2]int{{2000, 1125}, {1280, 720}, {1600, 900}, {1920, 1080}, {2560, 1440}, {3840, 2160}}
	for _, c := range cases {
		img := load(t, c.file)
		for _, s := range sizes {
			t.Run(fmt.Sprintf("%s/%dx%d", c.file, s[0], s[1]), func(t *testing.T) {
				if got := Classify(imageFrame{img, s[0], s[1]}); got != c.want {
					t.Errorf("got %v, want %v", got, c.want)
				}
			})
		}
	}
}

// A popup with a short message (not the "server full" one) is told apart.
func TestClassifyOtherPopup(t *testing.T) {
	img := load(t, "full_popup.jpg")
	f := &erased{imageFrame{img, 2000, 1125}, Box{0.36, 0.49, 0.64, 0.517}, 0.47, 0.53}
	if got := Classify(f); got != PopupOther {
		t.Errorf("got %v, want %v", got, PopupOther)
	}
}

// erased paints over part of a box with the panel colour, keeping only the
// text between keep0 and keep1.
type erased struct {
	imageFrame
	box          Box
	keep0, keep1 float64
}

func (f *erased) RGB(x, y int) (uint8, uint8, uint8) {
	fx := float64(x) / float64(f.w)
	if f.box.Contains(f.w, f.h, x, y) && (fx < f.keep0 || fx > f.keep1) {
		return 31, 31, 31
	}
	return f.imageFrame.RGB(x, y)
}

func TestBrightness(t *testing.T) {
	black := &BGRA{W: 100, H: 50, Stride: 400, Pix: make([]byte, 400*50)}
	if b := Brightness(black); b != 0 {
		t.Errorf("black frame brightness %v", b)
	}
	if b := Brightness(imageFrame{load(t, "browser.jpg"), 2000, 1125}); b < 20 {
		t.Errorf("browser brightness %v, want > 20", b)
	}
}

func TestServerList(t *testing.T) {
	// The highlighted Nasdara row in the screenshot.
	if !ServerList.Contains(2000, 1125, 700, 321) {
		t.Error("server row not inside ServerList")
	}
	if ServerList.Contains(2000, 1125, 1600, 940) {
		t.Error("PLAY button inside ServerList")
	}
}

func TestBGRA(t *testing.T) {
	f := &BGRA{W: 2, H: 1, Stride: 8, Pix: []byte{1, 2, 3, 255, 10, 20, 30, 255}}
	if r, g, b := f.RGB(1, 0); r != 30 || g != 20 || b != 10 {
		t.Errorf("got %d,%d,%d", r, g, b)
	}
}
