//go:build darwin && cgo

package clipboardimg

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

// 同时放 PNG 与 TIFF：PNG 是大多数应用（浏览器、预览、聊天工具）读的类型，
// 一部分老的 Cocoa 应用只认 TIFF。返回 0 表示成功，非 0 是失败的那一步。
static int agentreWritePNG(const void *bytes, long length) {
	@autoreleasepool {
		NSData *png = [NSData dataWithBytes:bytes length:(NSUInteger)length];
		NSImage *image = [[NSImage alloc] initWithData:png];
		if (image == nil) {
			return 1;
		}
		NSData *tiff = [image TIFFRepresentation];
		[image release];

		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		[pb clearContents];
		[pb declareTypes:@[NSPasteboardTypePNG, NSPasteboardTypeTIFF] owner:nil];
		if (![pb setData:png forType:NSPasteboardTypePNG]) {
			return 2;
		}
		if (tiff != nil && ![pb setData:tiff forType:NSPasteboardTypeTIFF]) {
			return 3;
		}
		return 0;
	}
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func writePNG(png []byte) error {
	if len(png) == 0 {
		return fmt.Errorf("clipboardimg: empty image")
	}
	switch rc := C.agentreWritePNG(unsafe.Pointer(&png[0]), C.long(len(png))); rc {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("clipboardimg: NSImage could not decode the PNG")
	default:
		return fmt.Errorf("clipboardimg: NSPasteboard setData failed (step %d)", int(rc))
	}
}
