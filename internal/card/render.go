package card

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// The card's size, a common one for link and chat previews.
const (
	Width  = 1200
	Height = 630
)

const (
	margin     = 60
	panelGap   = 60
	panelWidth = (Width - 2*margin - panelGap) / 2
	barsHeight = 150
	barGap     = 2
	barRadius  = 4
)

var (
	surface     = color.RGBA{0x1a, 0x1a, 0x19, 0xff}
	inkPrimary  = color.RGBA{0xf0, 0xef, 0xec, 0xff}
	inkMuted    = color.RGBA{0xa3, 0xa2, 0x9e, 0xff}
	rule        = color.RGBA{0x38, 0x38, 0x35, 0xff}
	accentDown  = color.RGBA{0x39, 0x87, 0xe5, 0xff}
	accentUp    = color.RGBA{0xd9, 0x59, 0x26, 0xff}
	regularFont = mustParse(goregular.TTF)
	boldFont    = mustParse(gobold.TTF)
)

func mustParse(ttf []byte) *opentype.Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err) // The fonts are embedded, so this can't happen in a built binary.
	}
	return f
}

// Render draws the card.
func Render(l Layout) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, Width, Height))
	draw.Draw(img, img.Bounds(), image.NewUniform(surface), image.Point{}, draw.Src)

	text(img, boldFont, 34, inkPrimary, margin, 76, "zap")
	textRight(img, regularFont, 22, inkMuted, Width-margin, 62, l.Server)
	textRight(img, regularFont, 22, inkMuted, Width-margin, 92, l.Date)

	panel(img, margin, "DOWNLOAD", l.Download, accentDown, l.DownloadSeries)
	panel(img, margin+panelWidth+panelGap, "UPLOAD", l.Upload, accentUp, l.UploadSeries)

	fill(img, image.Rect(margin, 482, Width-margin, 483), rule)
	stats := [][2]string{
		{"Idle latency", l.Idle},
		{"Jitter", l.Jitter},
		{"Loaded download", l.LoadedDownload},
		{"Loaded upload", l.LoadedUpload},
	}
	for i, s := range stats {
		x := margin + i*(Width-2*margin)/len(stats)
		text(img, regularFont, 20, inkMuted, x, 526, s[0])
		text(img, boldFont, 32, inkPrimary, x, 570, s[1])
	}
	return img
}

// panel draws one transfer phase. Text stays in ink colours, and the accent marks
// identify the series.
func panel(img *image.RGBA, x int, label, speed string, accent color.RGBA, series []float64) {
	fill(img, image.Rect(x, 134, x+14, 148), accent)
	text(img, boldFont, 20, inkMuted, x+24, 148, label)

	end := text(img, boldFont, 76, inkPrimary, x, 236, speed)
	text(img, regularFont, 30, inkMuted, end+12, 236, "Mbps")

	bars(img, image.Rect(x, 276, x+panelWidth, 276+barsHeight), accent, series)
	fill(img, image.Rect(x, 276+barsHeight, x+panelWidth, 276+barsHeight+1), rule)
	text(img, regularFont, 18, inkMuted, x, 276+barsHeight+28, "Speed each second, scaled from zero to the peak")
}

// bars draws a thin bar for each value, anchored to the bottom of r and scaled
// from zero to the highest value.
func bars(img *image.RGBA, r image.Rectangle, accent color.RGBA, values []float64) {
	var highest float64
	for _, v := range values {
		highest = max(highest, v)
	}
	if highest <= 0 {
		return
	}
	step := float32(r.Dx()+barGap) / float32(len(values))
	width := max(step-barGap, 1)
	for i, v := range values {
		if v <= 0 {
			continue
		}
		height := max(float32(v/highest)*float32(r.Dy()), 2)
		x := float32(r.Min.X) + float32(i)*step
		roundedBar(img, x, float32(r.Max.Y)-height, width, height, accent)
	}
}

// roundedBar draws a bar with rounded top corners and a square base.
func roundedBar(img *image.RGBA, x, y, w, h float32, c color.RGBA) {
	radius := min(float32(barRadius), w/2, h)
	b := img.Bounds()
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	z.MoveTo(x, y+h)
	z.LineTo(x, y+radius)
	z.QuadTo(x, y, x+radius, y)
	z.LineTo(x+w-radius, y)
	z.QuadTo(x+w, y, x+w, y+radius)
	z.LineTo(x+w, y+h)
	z.ClosePath()
	z.Draw(img, b, image.NewUniform(c), image.Point{})
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
}

func newFace(f *opentype.Font, size float64) font.Face {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic(err) // Only happens with an invalid size, which are all constants here.
	}
	return face
}

// text draws s with its baseline at y, and returns the x position where it ends.
func text(img *image.RGBA, f *opentype.Font, size float64, c color.RGBA, x, y int, s string) int {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: newFace(f, size), Dot: fixed.P(x, y)}
	d.DrawString(s)
	return d.Dot.X.Ceil()
}

func textRight(img *image.RGBA, f *opentype.Font, size float64, c color.RGBA, right, y int, s string) {
	width := font.MeasureString(newFace(f, size), s).Ceil()
	text(img, f, size, c, right-width, y, s)
}
