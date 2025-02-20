// Copyright 2021 Matrix Origin
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"time"

	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/options"
)

type CacheSizeType uint8

const (
	CST_None CacheSizeType = iota
	CST_Customize
)

type BlockSizeType uint8

const (
	BST_None BlockSizeType = iota
	BST_S
	BST_M
	BST_L
)

var blockSizes map[BlockSizeType]uint32 = map[BlockSizeType]uint32{
	BST_None: options.DefaultBlockMaxRows,
	BST_S:    uint32(16),
	BST_M:    uint32(1600),
	BST_L:    uint32(160000),
}

type SegmentSizeType uint8

const (
	SST_None SegmentSizeType = iota
	SST_S
	SST_M
	SST_L
)

var segmentSizes map[SegmentSizeType]uint16 = map[SegmentSizeType]uint16{
	SST_None: options.DefaultBlocksPerSegment,
	SST_S:    uint16(4),
	SST_M:    uint16(40),
	SST_L:    uint16(400),
}

func NewOptions(dir string, cst CacheSizeType, bst BlockSizeType, sst SegmentSizeType) *options.Options {
	blockSize := blockSizes[bst]
	blockCnt := segmentSizes[sst]
	opts := new(options.Options)
	storageCfg := new(options.StorageCfg)
	storageCfg.BlockMaxRows = blockSize
	storageCfg.SegmentMaxBlocks = blockCnt
	opts.StorageCfg = storageCfg

	if cst == CST_Customize {
		cacheCfg := new(options.CacheCfg)
		cacheCfg.IndexCapacity = uint64(blockSize) * uint64(blockCnt) * 80
		opts.CacheCfg = cacheCfg
	}
	opts.FillDefaults(dir)
	return opts
}

func NewCustomizedMetaOptions(dir string, cst CacheSizeType, blockRows uint32, blockCnt uint16, opts *options.Options) *options.Options {
	if opts == nil {
		opts = new(options.Options)
	}
	storageCfg := &options.StorageCfg{
		BlockMaxRows:     blockRows,
		SegmentMaxBlocks: blockCnt,
	}
	opts.StorageCfg = storageCfg
	if cst == CST_Customize {
		cacheCfg := new(options.CacheCfg)
		cacheCfg.IndexCapacity = uint64(blockRows) * uint64(blockCnt) * 2000
		opts.CacheCfg = cacheCfg
	}
	opts.FillDefaults(dir)
	return opts
}

func WithQuickScanAndCKPOpts2(in *options.Options, factor int) (opts *options.Options) {
	opts = WithQuickScanAndCKPOpts(in)
	opts.CheckpointCfg.ScanInterval *= time.Duration(factor)
	opts.CheckpointCfg.FlushInterval *= time.Duration(factor)
	opts.CheckpointCfg.MinCount = int64(factor)
	opts.CheckpointCfg.IncrementalInterval *= time.Duration(factor)
	return opts
}

func WithQuickScanAndCKPOpts(in *options.Options) (opts *options.Options) {
	if in == nil {
		opts = new(options.Options)
	} else {
		opts = in
	}
	opts.CheckpointCfg = new(options.CheckpointCfg)
	opts.CheckpointCfg.ScanInterval = time.Millisecond * 10
	opts.CheckpointCfg.FlushInterval = time.Millisecond * 10
	opts.CheckpointCfg.MinCount = 1
	opts.CheckpointCfg.IncrementalInterval = time.Millisecond * 20
	opts.CheckpointCfg.GlobalMinCount = 1
	opts.CheckpointCfg.GCCheckpointInterval = time.Millisecond * 10
	opts.GCCfg = new(options.GCCfg)
	opts.GCCfg.ScanGCInterval = time.Millisecond * 10
	opts.GCCfg.GCTTL = time.Millisecond * 1
	opts.CatalogCfg = new(options.CatalogCfg)
	opts.CatalogCfg.GCInterval = time.Millisecond * 1
	return opts
}

func WithQuickScanAndCKPAndGCOpts(in *options.Options) (opts *options.Options) {
	if in == nil {
		opts = new(options.Options)
	} else {
		opts = in
	}
	opts.CheckpointCfg = new(options.CheckpointCfg)
	opts.CheckpointCfg.ScanInterval = time.Millisecond * 10
	opts.CheckpointCfg.FlushInterval = time.Millisecond * 10
	opts.CheckpointCfg.MinCount = 1
	opts.CheckpointCfg.IncrementalInterval = time.Millisecond * 20
	opts.CheckpointCfg.GlobalMinCount = 1

	opts.GCCfg = new(options.GCCfg)
	// ScanGCInterval does not need to be too fast, because manual gc will be performed in the case
	opts.GCCfg.ScanGCInterval = time.Second * 10
	opts.CatalogCfg = new(options.CatalogCfg)
	opts.CatalogCfg.GCInterval = time.Millisecond * 1
	opts.GCCfg.GCTTL = time.Millisecond * 1
	return opts
}

func WithOpts(in *options.Options, factor float64) (opts *options.Options) {
	if in == nil {
		opts = new(options.Options)
	} else {
		opts = in
	}
	opts.CheckpointCfg = new(options.CheckpointCfg)
	opts.CheckpointCfg.ScanInterval = time.Second * time.Duration(factor)
	opts.CheckpointCfg.FlushInterval = time.Second * time.Duration(factor)
	opts.CheckpointCfg.MinCount = 1 * int64(factor)
	opts.CheckpointCfg.IncrementalInterval = time.Second * 2 * time.Duration(factor)
	opts.CheckpointCfg.GlobalMinCount = 10
	return opts
}

func WithLongScanAndCKPOpts(in *options.Options) (opts *options.Options) {
	if in == nil {
		opts = new(options.Options)
	} else {
		opts = in
	}
	opts.CheckpointCfg = new(options.CheckpointCfg)
	opts.CheckpointCfg.ScanInterval = time.Hour
	opts.CheckpointCfg.MinCount = 100000000
	opts.CheckpointCfg.IncrementalInterval = time.Hour
	opts.CheckpointCfg.GlobalMinCount = 10000000
	return opts
}
