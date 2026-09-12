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
// createtime / updatetime 五个业务列，加上自增主键 id。规格「数据」一节列的字段
// 就是这五个——本表不设「所有者标识」列，理由见 port_forward.go 顶部包注释。
func TestPortForward_Fields(t *testing.T) {
	p := &PortForward{ID: 1, Port: 3000, Name: "dev server", Enabled: true, Createtime: 10, Updatetime: 20}
	assert.Equal(t, int64(1), p.ID)
	assert.Equal(t, 3000, p.Port)
	assert.Equal(t, "dev server", p.Name)
	assert.True(t, p.Enabled)
	assert.Equal(t, int64(10), p.Createtime)
	assert.Equal(t, int64(20), p.Updatetime)

	tp := reflect.TypeOf(*p)
	wantColumns := map[string]string{
		"ID":         "id",
		"Port":       "port",
		"Name":       "name",
		"Enabled":    "enabled",
		"Createtime": "createtime",
		"Updatetime": "updatetime",
	}
	for field, column := range wantColumns {
		f, ok := tp.FieldByName(field)
		assert.True(t, ok, "field %s must exist on PortForward", field)
		assert.Contains(t, f.Tag.Get("gorm"), "column:"+column, "field %s must map to column %s", field, column)
	}
}
