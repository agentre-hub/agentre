package clipboardimg

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestIsPNG(t *testing.T) {
	Convey("Given clipboard payload candidates", t, func() {
		Convey("when the bytes start with the PNG signature, then it is a PNG", func() {
			img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
			So(IsPNG(encodePNG(t, img)), ShouldBeTrue)
		})

		Convey("when the bytes are a JPEG header, then it is not a PNG", func() {
			So(IsPNG([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46}), ShouldBeFalse)
		})

		Convey("when the payload is empty or shorter than the signature, then it is not a PNG", func() {
			So(IsPNG(nil), ShouldBeFalse)
			So(IsPNG([]byte{0x89, 'P', 'N'}), ShouldBeFalse)
		})
	})
}

func TestPNGToDIBV5(t *testing.T) {
	Convey("Given a 2×2 PNG with distinct straight-alpha pixels", t, func() {
		img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		img.SetNRGBA(0, 0, color.NRGBA{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF}) // 左上 红
		img.SetNRGBA(1, 0, color.NRGBA{R: 0x00, G: 0x00, B: 0xFF, A: 0x80}) // 右上 半透明蓝
		img.SetNRGBA(0, 1, color.NRGBA{R: 0x00, G: 0xFF, B: 0x00, A: 0xFF}) // 左下 绿
		img.SetNRGBA(1, 1, color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0x00}) // 右下 全透明

		Convey("when converted to CF_DIBV5, then the header describes a bottom-up 32-bit BGRA bitmap with alpha", func() {
			dib, err := PNGToDIBV5(encodePNG(t, img))
			So(err, ShouldBeNil)
			So(len(dib), ShouldEqual, 124+2*2*4)

			le := binary.LittleEndian
			So(le.Uint32(dib[0:]), ShouldEqual, 124)         // bV5Size
			So(int32(le.Uint32(dib[4:])), ShouldEqual, 2)    // bV5Width
			So(int32(le.Uint32(dib[8:])), ShouldEqual, 2)    // bV5Height > 0 = bottom-up
			So(le.Uint16(dib[12:]), ShouldEqual, 1)          // bV5Planes
			So(le.Uint16(dib[14:]), ShouldEqual, 32)         // bV5BitCount
			So(le.Uint32(dib[16:]), ShouldEqual, 3)          // BI_BITFIELDS
			So(le.Uint32(dib[20:]), ShouldEqual, 16)         // bV5SizeImage
			So(le.Uint32(dib[40:]), ShouldEqual, 0x00FF0000) // red mask
			So(le.Uint32(dib[44:]), ShouldEqual, 0x0000FF00) // green mask
			So(le.Uint32(dib[48:]), ShouldEqual, 0x000000FF) // blue mask
			So(le.Uint32(dib[52:]), ShouldEqual, 0xFF000000) // alpha mask
			So(le.Uint32(dib[56:]), ShouldEqual, 0x73524742) // LCS_sRGB 'sRGB'

			Convey("and the pixel rows are stored bottom row first, each pixel as B,G,R,A", func() {
				px := dib[124:]
				// 第一行存的是图片最下面那一行：绿、全透明。
				So(px[0:4], ShouldResemble, []byte{0x00, 0xFF, 0x00, 0xFF})
				So(px[4:8], ShouldResemble, []byte{0x30, 0x20, 0x10, 0x00})
				// 第二行是图片最上面那一行：红、半透明蓝。
				So(px[8:12], ShouldResemble, []byte{0x00, 0x00, 0xFF, 0xFF})
				So(px[12:16], ShouldResemble, []byte{0xFF, 0x00, 0x00, 0x80})
			})
		})
	})

	Convey("Given bytes that are not a decodable PNG", t, func() {
		Convey("when converted, then it returns an error instead of a bitmap", func() {
			dib, err := PNGToDIBV5([]byte("not a png"))
			So(err, ShouldNotBeNil)
			So(dib, ShouldBeNil)
		})
	})
}
