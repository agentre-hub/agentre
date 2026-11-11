package clipboardimg

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/png"
)

const (
	dibV5HeaderSize = 124
	biBitfields     = 3
	lcsSRGB         = 0x73524742 // 'sRGB'
	lcsGMImages     = 4          // LCS_GM_IMAGES
)

// PNGToDIBV5 把 PNG 解成 Windows 剪贴板的 CF_DIBV5：BITMAPV5HEADER 紧跟像素，
// 32 位 BGRA、自下而上、直通 alpha（不预乘）。
//
// 不带平台标签，任何平台都能测；只有 write_windows.go 用它。
func PNGToDIBV5(data []byte) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("clipboardimg: decode png: %w", err)
	}
	bounds := src.Bounds()
	img := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(img, img.Bounds(), src, bounds.Min, draw.Src)

	width, height := img.Rect.Dx(), img.Rect.Dy()
	pixelBytes := width * height * 4
	out := make([]byte, dibV5HeaderSize+pixelBytes)

	le := binary.LittleEndian
	le.PutUint32(out[0:], dibV5HeaderSize)
	le.PutUint32(out[4:], uint32(int32(width)))
	le.PutUint32(out[8:], uint32(int32(height))) // 正高 = 自下而上
	le.PutUint16(out[12:], 1)                    // planes
	le.PutUint16(out[14:], 32)                   // bit count
	le.PutUint32(out[16:], biBitfields)
	le.PutUint32(out[20:], uint32(pixelBytes))
	le.PutUint32(out[40:], 0x00FF0000)   // red mask
	le.PutUint32(out[44:], 0x0000FF00)   // green mask
	le.PutUint32(out[48:], 0x000000FF)   // blue mask
	le.PutUint32(out[52:], 0xFF000000)   // alpha mask
	le.PutUint32(out[56:], lcsSRGB)      // color space
	le.PutUint32(out[108:], lcsGMImages) // intent

	px := out[dibV5HeaderSize:]
	for y := range height {
		srcRow := img.Pix[y*img.Stride : y*img.Stride+width*4]
		dstRow := px[(height-1-y)*width*4:]
		for x := range width {
			r, g, b, a := srcRow[x*4], srcRow[x*4+1], srcRow[x*4+2], srcRow[x*4+3]
			dstRow[x*4], dstRow[x*4+1], dstRow[x*4+2], dstRow[x*4+3] = b, g, r, a
		}
	}
	return out, nil
}
