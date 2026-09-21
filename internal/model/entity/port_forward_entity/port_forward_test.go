package port_forward_entity

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPortForward_TableName 钉住表名——迁移与仓储都按这个名字操作同一张表。
func TestPortForward_TableName(t *testing.T) {
	assert.Equal(t, "port_forwards", (&PortForward{}).TableName())
}

// TestPortForward_Fields 覆盖字段形状与 gorm 列名标注：port / name / enabled /
// createtime / updatetime / target / insecure 七个业务列，加上自增主键 id。
// target / insecure 是规格「映射与目标」一节加的两格——目标现在是一条规范化字符串
// （"http(s)://host:port"，总带着端口），insecure 只对 https 目标有意义。port 仍然
// 保留：它是 target 里那个端口，只为列举时展示与排序（open 按 id 定位、拨 target）。
func TestPortForward_Fields(t *testing.T) {
	p := &PortForward{
		ID: 1, Port: 3000, Name: "dev server", Enabled: true,
		Target: "http://127.0.0.1:3000", Insecure: false,
		Createtime: 10, Updatetime: 20,
	}
	assert.Equal(t, int64(1), p.ID)
	assert.Equal(t, 3000, p.Port)
	assert.Equal(t, "dev server", p.Name)
	assert.True(t, p.Enabled)
	assert.Equal(t, "http://127.0.0.1:3000", p.Target)
	assert.False(t, p.Insecure)
	assert.Equal(t, int64(10), p.Createtime)
	assert.Equal(t, int64(20), p.Updatetime)

	tp := reflect.TypeOf(*p)
	wantColumns := map[string]string{
		"ID":         "id",
		"Port":       "port",
		"Name":       "name",
		"Enabled":    "enabled",
		"Target":     "target",
		"Insecure":   "insecure",
		"Createtime": "createtime",
		"Updatetime": "updatetime",
	}
	for field, column := range wantColumns {
		f, ok := tp.FieldByName(field)
		assert.True(t, ok, "field %s must exist on PortForward", field)
		assert.Contains(t, f.Tag.Get("gorm"), "column:"+column, "field %s must map to column %s", field, column)
	}
}
