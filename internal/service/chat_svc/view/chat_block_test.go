package view

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/canonical"
)

func TestCanonicalDTO_MarshalsKindDiscriminator(t *testing.T) {
	Convey("CanonicalDTO 序列化时 kind 判别式在前,未命中的变体槽省略", t, func() {
		fw := canonical.FileWrite{Path: "/tmp/x", Content: "data"}
		raw, err := json.Marshal(CanonicalDTO{Kind: canonical.KindFileWrite, FileWrite: &fw})
		So(err, ShouldBeNil)
		So(string(raw), ShouldStartWith, `{"kind":"file.write","fileWrite":{`)
		So(string(raw), ShouldNotContainSubstring, `"fileEdit"`)
		So(string(raw), ShouldNotContainSubstring, `"planUpdate"`)
	})
}
