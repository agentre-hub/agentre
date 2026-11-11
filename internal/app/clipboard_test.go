package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestCopyImageToClipboard(t *testing.T) {
	Convey("Given a stubbed platform image writer", t, func() {
		var got [][]byte
		writeErr := error(nil)
		orig := writeClipboardPNG
		writeClipboardPNG = func(data []byte) error {
			got = append(got, data)
			return writeErr
		}
		defer func() { writeClipboardPNG = orig }()

		a := &App{ctx: context.Background()}
		pngBytes := tinyPNG(t)

		Convey("when a base64 PNG arrives, then the exact bytes reach the platform writer once", func() {
			err := a.CopyImageToClipboard(base64.StdEncoding.EncodeToString(pngBytes))
			So(err, ShouldBeNil)
			So(got, ShouldHaveLength, 1)
			So(got[0], ShouldResemble, pngBytes)
		})

		Convey("when the payload is empty, then it errors and nothing is written", func() {
			err := a.CopyImageToClipboard("")
			So(err, ShouldNotBeNil)
			So(got, ShouldBeEmpty)
		})

		Convey("when the payload is not a PNG, then it errors and nothing is written", func() {
			jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}
			err := a.CopyImageToClipboard(base64.StdEncoding.EncodeToString(jpeg))
			So(err, ShouldNotBeNil)
			So(got, ShouldBeEmpty)
		})

		Convey("when the payload is not valid base64, then it errors and nothing is written", func() {
			err := a.CopyImageToClipboard("%%%not-base64%%%")
			So(err, ShouldNotBeNil)
			So(got, ShouldBeEmpty)
		})

		Convey("when the platform writer fails, then its error is returned unchanged", func() {
			writeErr = errors.New("NSPasteboard setData failed")
			err := a.CopyImageToClipboard(base64.StdEncoding.EncodeToString(pngBytes))
			So(err, ShouldEqual, writeErr)
		})
	})
}
