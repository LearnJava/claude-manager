// genicon renders the app icon (rounded tile + two overlapping "session
// card" squares) and writes build/appicon.png plus a multi-size
// build/windows/icon.ico. Run manually with `go run ./cmd/genicon` when the
// icon needs to change; it is not part of the normal build.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
)

const (
	ss         = 4 // supersampling factor for anti-aliasing
	cornerFrac = 0.22
	bgHex      = 0x171717
	fgHex      = 0x2DD4BF // teal accent (front card)
	dimHex     = 0x216C63 // muted teal (back card)
)

var icoSizes = []int{256, 128, 64, 48, 32, 16}

func main() {
	// Full-resolution app icon.
	appIcon := render(1024)
	writePNG("build/appicon.png", appIcon)

	// Multi-size Windows .ico (also used directly as the tray icon).
	frames := make([][]byte, len(icoSizes))
	for i, s := range icoSizes {
		img := render(s)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			log.Fatalf("encode %d: %v", s, err)
		}
		frames[i] = buf.Bytes()
	}
	writeICO("build/windows/icon.ico", icoSizes, frames)

	log.Println("done")
}

// render draws the icon at size x size using ss x supersampling, then
// box-downsamples to the target resolution.
func render(size int) *image.RGBA {
	big := size * ss
	canvas := image.NewRGBA(image.Rect(0, 0, big, big))

	bg := hexColor(bgHex)
	fg := hexColor(fgHex)
	dim := hexColor(dimHex)
	radius := float64(big) * cornerFrac

	for y := 0; y < big; y++ {
		for x := 0; x < big; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			if !insideRoundedSquare(fx, fy, float64(big), radius) {
				continue // leave transparent
			}
			canvas.Set(x, y, bg)
		}
	}

	drawGlyph(canvas, big, fg, dim)

	return downsample(canvas, size, ss)
}

// insideRoundedSquare reports whether (x,y) falls within a size x size square
// with rounded corners of the given radius.
func insideRoundedSquare(x, y, size, radius float64) bool {
	// Clamp point into the "core" rect shrunk by radius, then it's always
	// inside; otherwise test distance to the nearest corner circle centre.
	minC, maxC := radius, size-radius
	cx, cy := x, y
	if x < minC {
		cx = minC
	} else if x > maxC {
		cx = maxC
	}
	if y < minC {
		cy = minC
	} else if y > maxC {
		cy = maxC
	}
	dx, dy := x-cx, y-cy
	if dx == 0 && dy == 0 {
		return true
	}
	return dx*dx+dy*dy <= radius*radius
}

// drawGlyph paints two overlapping rounded-square "cards", evoking multiple
// parallel sessions/windows stacked on top of one another — the app manages
// several concurrent Claude Code sessions at once.
func drawGlyph(canvas *image.RGBA, big int, fg, dim color.RGBA) {
	f := float64(big)
	cardSize := f * 0.42
	cardRadius := cardSize * 0.28

	// Back card (upper-left), muted.
	fillRoundedRect(canvas, f*0.20, f*0.20, cardSize, cardRadius, dim)
	// Front card (lower-right), full accent — drawn last so it overlaps.
	fillRoundedRect(canvas, f*0.38, f*0.38, cardSize, cardRadius, fg)
}

// fillRoundedRect paints a size x size rounded square whose top-left corner
// is at (ox, oy).
func fillRoundedRect(canvas *image.RGBA, ox, oy, size, radius float64, col color.RGBA) {
	b := canvas.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			fx, fy := float64(x)+0.5-ox, float64(y)+0.5-oy
			if insideRoundedSquare(fx, fy, size, radius) {
				canvas.Set(x, y, col)
			}
		}
	}
}

func hexColor(h uint32) color.RGBA {
	return color.RGBA{
		R: uint8(h >> 16),
		G: uint8(h >> 8),
		B: uint8(h),
		A: 255,
	}
}

// downsample box-filters a size*factor canvas down to size x size, averaging
// premultiplied alpha to avoid dark fringing on the transparent corners.
func downsample(src *image.RGBA, size, factor int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a uint32
			for sy := 0; sy < factor; sy++ {
				for sx := 0; sx < factor; sx++ {
					c := src.RGBAAt(x*factor+sx, y*factor+sy)
					pr, pg, pb, pa := uint32(c.R), uint32(c.G), uint32(c.B), uint32(c.A)
					r += pr * pa / 255
					g += pg * pa / 255
					b += pb * pa / 255
					a += pa
				}
			}
			n := uint32(factor * factor)
			a /= n
			if a == 0 {
				dst.SetRGBA(x, y, color.RGBA{})
				continue
			}
			r = r / n * 255 / a
			g = g / n * 255 / a
			b = b / n * 255 / a
			dst.SetRGBA(x, y, color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)})
		}
	}
	return dst
}

func writePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatalf("encode %s: %v", path, err)
	}
}

// writeICO assembles a Windows .ico with PNG-compressed frames (supported
// since Vista for any size, including the classic 256x256).
func writeICO(path string, sizes []int, pngFrames [][]byte) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	// ICONDIR
	binary.Write(f, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(f, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(f, binary.LittleEndian, uint16(len(sizes)))

	headerSize := 6 + 16*len(sizes)
	offset := uint32(headerSize)

	for i, s := range sizes {
		dim := byte(s)
		if s >= 256 {
			dim = 0 // 0 means 256 in ICONDIRENTRY
		}
		f.Write([]byte{dim, dim, 0, 0}) // width, height, colors, reserved
		binary.Write(f, binary.LittleEndian, uint16(1))  // color planes
		binary.Write(f, binary.LittleEndian, uint16(32)) // bits per pixel
		binary.Write(f, binary.LittleEndian, uint32(len(pngFrames[i])))
		binary.Write(f, binary.LittleEndian, offset)
		offset += uint32(len(pngFrames[i]))
	}

	for _, data := range pngFrames {
		f.Write(data)
	}
}
