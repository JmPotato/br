// Copyright 2021 PingCAP, Inc. Licensed under Apache-2.0.

package checksum

import (
	"context"
	"time"

	"github.com/pingcap/errors"
	backuppb "github.com/pingcap/kvproto/pkg/backup"
	"github.com/pingcap/log"
	"go.uber.org/zap"

	berrors "github.com/pingcap/br/pkg/errors"
	"github.com/pingcap/br/pkg/metautil"
	"github.com/pingcap/br/pkg/storage"
	"github.com/pingcap/br/pkg/summary"
)

// FastChecksum checks whether the "local" checksum matches the checksum from TiKV.
func FastChecksum(
	ctx context.Context, backupMeta *backuppb.BackupMeta, storage storage.ExternalStorage,
) error {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		summary.CollectDuration("backup fast checksum", elapsed)
	}()

	ch := make(chan *metautil.Table)
	errCh := make(chan error)
	go func() {
		reader := metautil.NewMetaReader(backupMeta, storage)
		if err := reader.ReadSchemasFiles(ctx, ch); err != nil {
			errCh <- errors.Trace(err)
		}
		close(ch)
	}()

	for {
		var tbl *metautil.Table
		var ok bool
		select {
		case <-ctx.Done():
			return errors.Trace(ctx.Err())
		case tbl, ok = <-ch:
			if !ok {
				close(errCh)
				return nil
			}
		}
		checksum := uint64(0)
		totalKvs := uint64(0)
		totalBytes := uint64(0)
		for _, file := range tbl.Files {
			checksum ^= file.Crc64Xor
			totalKvs += file.TotalKvs
			totalBytes += file.TotalBytes
		}

		if checksum != tbl.Crc64Xor ||
			totalBytes != tbl.TotalBytes ||
			totalKvs != tbl.TotalKvs {
			log.Error("checksum mismatch",
				zap.Stringer("db", tbl.DB.Name),
				zap.Stringer("table", tbl.Info.Name),
				zap.Uint64("origin tidb crc64", tbl.Crc64Xor),
				zap.Uint64("calculated crc64", checksum),
				zap.Uint64("origin tidb total kvs", tbl.TotalKvs),
				zap.Uint64("calculated total kvs", totalKvs),
				zap.Uint64("origin tidb total bytes", tbl.TotalBytes),
				zap.Uint64("calculated total bytes", totalBytes))
			// TODO enhance error
			return errors.Trace(berrors.ErrBackupChecksumMismatch)
		}
		log.Info("checksum success",
			zap.Stringer("db", tbl.DB.Name), zap.Stringer("table", tbl.Info.Name))
	}
}
