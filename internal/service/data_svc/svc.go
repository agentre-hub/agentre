package data_svc

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// DataSvc 数据导入导出 service。
type DataSvc interface {
	Export(ctx context.Context, req *ExportRequest) (*ExportResult, error)
	PreviewImport(ctx context.Context, raw []byte) (*ImportPreview, error)
	ApplyImport(ctx context.Context, req *ApplyImportRequest) (*ApplyImportResult, error)
}

type dataSvc struct {
	now     func() int64
	newUUID func() string
}

var defaultSvc DataSvc = &dataSvc{
	now:     func() int64 { return time.Now().UnixMilli() },
	newUUID: uuid.NewString,
}

// Default 返回默认实现。
func Default() DataSvc { return defaultSvc }
