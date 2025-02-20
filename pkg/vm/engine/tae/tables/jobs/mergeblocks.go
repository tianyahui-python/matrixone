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

package jobs

import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"github.com/matrixorigin/matrixone/pkg/objectio"

	"github.com/RoaringBitmap/roaring"
	"github.com/matrixorigin/matrixone/pkg/logutil"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/blockio"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/catalog"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/common"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/containers"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/iface/handle"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/iface/txnif"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/mergesort"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/model"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/tables/txnentries"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/tasks"
	"go.uber.org/zap/zapcore"
)

// CompactSegmentTaskFactory merge non-appendable blocks of an appendable-segment
// into a new non-appendable segment.
var CompactSegmentTaskFactory = func(mergedBlks []*catalog.BlockEntry, scheduler tasks.TaskScheduler) tasks.TxnTaskFactory {
	return func(ctx *tasks.Context, txn txnif.AsyncTxn) (tasks.Task, error) {
		mergedSegs := make([]*catalog.SegmentEntry, 1)
		mergedSegs[0] = mergedBlks[0].GetSegment()
		return NewMergeBlocksTask(ctx, txn, mergedBlks, mergedSegs, nil, scheduler)
	}
}

var MergeBlocksIntoSegmentTaskFctory = func(mergedBlks []*catalog.BlockEntry, toSegEntry *catalog.SegmentEntry, scheduler tasks.TaskScheduler) tasks.TxnTaskFactory {
	return func(ctx *tasks.Context, txn txnif.AsyncTxn) (tasks.Task, error) {
		return NewMergeBlocksTask(ctx, txn, mergedBlks, nil, toSegEntry, scheduler)
	}
}

type mergeBlocksTask struct {
	*tasks.BaseTask
	txn         txnif.AsyncTxn
	toSegEntry  *catalog.SegmentEntry
	createdSegs []*catalog.SegmentEntry
	mergedSegs  []*catalog.SegmentEntry
	mergedBlks  []*catalog.BlockEntry
	createdBlks []*catalog.BlockEntry
	compacted   []handle.Block
	rel         handle.Relation
	scheduler   tasks.TaskScheduler
	scopes      []common.ID
	deletes     []*roaring.Bitmap
}

func NewMergeBlocksTask(ctx *tasks.Context, txn txnif.AsyncTxn, mergedBlks []*catalog.BlockEntry, mergedSegs []*catalog.SegmentEntry, toSegEntry *catalog.SegmentEntry, scheduler tasks.TaskScheduler) (task *mergeBlocksTask, err error) {
	task = &mergeBlocksTask{
		txn:         txn,
		mergedBlks:  mergedBlks,
		mergedSegs:  mergedSegs,
		createdBlks: make([]*catalog.BlockEntry, 0),
		compacted:   make([]handle.Block, 0),
		scheduler:   scheduler,
		toSegEntry:  toSegEntry,
	}
	dbId := mergedBlks[0].GetSegment().GetTable().GetDB().ID
	database, err := txn.GetDatabaseByID(dbId)
	if err != nil {
		return
	}
	relId := mergedBlks[0].GetSegment().GetTable().ID
	task.rel, err = database.GetRelationByID(relId)
	if err != nil {
		return
	}
	for _, meta := range mergedBlks {
		seg, err := task.rel.GetSegment(&meta.GetSegment().ID)
		if err != nil {
			return nil, err
		}
		blk, err := seg.GetBlock(meta.ID)
		if err != nil {
			return nil, err
		}
		task.compacted = append(task.compacted, blk)
		task.scopes = append(task.scopes, *meta.AsCommonID())
	}
	task.BaseTask = tasks.NewBaseTask(task, tasks.DataCompactionTask, ctx)
	return
}

func (task *mergeBlocksTask) Scopes() []common.ID { return task.scopes }

func (task *mergeBlocksTask) mergeColumn(
	vecs []containers.Vector,
	sortedIdx *[]uint32,
	isPrimary bool,
	fromLayout,
	toLayout []uint32,
	sort bool) (column []containers.Vector, mapping []uint32) {
	if len(vecs) == 0 {
		return
	}
	if sort {
		if isPrimary {
			column, mapping = mergesort.MergeSortedColumn(vecs, sortedIdx, fromLayout, toLayout)
		} else {
			column = mergesort.ShuffleColumn(vecs, *sortedIdx, fromLayout, toLayout)
		}
	} else {
		column, mapping = task.mergeColumnWithOutSort(vecs, fromLayout, toLayout)
	}
	for _, vec := range vecs {
		vec.Close()
	}
	return
}

func (task *mergeBlocksTask) mergeColumnWithOutSort(column []containers.Vector, fromLayout, toLayout []uint32) (ret []containers.Vector, mapping []uint32) {
	totalLength := uint32(0)
	for _, i := range toLayout {
		totalLength += i
	}
	mapping = make([]uint32, totalLength)
	for i := range mapping {
		mapping[i] = uint32(i)
	}
	ret = mergesort.Reshape(column, fromLayout, toLayout)
	return
}

func (task *mergeBlocksTask) MarshalLogObject(enc zapcore.ObjectEncoder) (err error) {
	blks := ""
	for _, blk := range task.mergedBlks {
		blks = fmt.Sprintf("%s%s,", blks, blk.ID.String())
	}
	enc.AddString("from-blks", blks)
	segs := ""
	for _, seg := range task.mergedSegs {
		segs = fmt.Sprintf("%s%s,", segs, seg.ID.ToString())
	}
	enc.AddString("from-segs", segs)

	toblks := ""
	for _, blk := range task.createdBlks {
		toblks = fmt.Sprintf("%s%s,", toblks, blk.ID.String())
	}
	if toblks != "" {
		enc.AddString("to-blks", toblks)
	}

	tosegs := ""
	for _, seg := range task.createdSegs {
		tosegs = fmt.Sprintf("%s%s,", tosegs, seg.ID.ToString())
	}
	if tosegs != "" {
		enc.AddString("to-segs", tosegs)
	}
	return
}

func (task *mergeBlocksTask) Execute() (err error) {
	logutil.Info("[Start] Mergeblocks", common.OperationField(task.Name()),
		common.OperandField(task))
	now := time.Now()
	var toSegEntry handle.Segment
	if task.toSegEntry == nil {
		if toSegEntry, err = task.rel.CreateNonAppendableSegment(false); err != nil {
			return err
		}
		task.toSegEntry = toSegEntry.GetMeta().(*catalog.SegmentEntry)
		task.toSegEntry.SetSorted()
		task.createdSegs = append(task.createdSegs, task.toSegEntry)
	} else {
		panic("warning: merge to a existing segment")
		// if toSegEntry, err = task.rel.GetSegment(task.toSegEntry.ID); err != nil {
		// 	return
		// }
	}

	// merge data according to the schema at startTs
	schema := task.rel.Schema().(*catalog.Schema)
	var view *model.ColumnView
	sortVecs := make([]containers.Vector, 0)
	rows := make([]uint32, 0)
	skipBlks := make([]int, 0)
	length := 0
	fromAddr := make([]uint32, 0, len(task.compacted))
	ids := make([]*common.ID, 0, len(task.compacted))
	task.deletes = make([]*roaring.Bitmap, len(task.compacted))

	// Prepare sort key resources
	// If there's no sort key, use physical address key
	var sortColDef *catalog.ColDef
	if schema.HasSortKey() {
		sortColDef = schema.GetSingleSortKey()
	} else {
		sortColDef = schema.PhyAddrKey
	}
	logutil.Infof("Mergeblocks on sort column %s\n", sortColDef.Name)

	idxes := make([]uint16, 0, len(schema.ColDefs)-1)
	seqnums := make([]uint16, 0, len(schema.ColDefs)-1)
	for _, def := range schema.ColDefs {
		if def.IsPhyAddr() {
			continue
		}
		idxes = append(idxes, uint16(def.Idx))
		seqnums = append(seqnums, def.SeqNum)
	}
	for _, block := range task.compacted {
		err = block.Prefetch(idxes)
		if err != nil {
			return
		}
	}

	for i, block := range task.compacted {
		if view, err = block.GetColumnDataById(sortColDef.Idx); err != nil {
			return
		}
		defer view.Close()
		task.deletes[i] = view.DeleteMask
		view.ApplyDeletes()
		vec := view.Orphan()
		defer vec.Close()
		if vec.Length() == 0 {
			skipBlks = append(skipBlks, i)
			continue
		}
		sortVecs = append(sortVecs, vec)
		rows = append(rows, uint32(vec.Length()))
		fromAddr = append(fromAddr, uint32(length))
		length += vec.Length()
		ids = append(ids, block.Fingerprint())
	}

	to := make([]uint32, 0)
	maxrow := schema.BlockMaxRows
	totalRows := length
	for totalRows > 0 {
		if totalRows > int(maxrow) {
			to = append(to, maxrow)
			totalRows -= int(maxrow)
		} else {
			to = append(to, uint32(totalRows))
			break
		}
	}

	// merge sort the sort key
	node, err := common.DefaultAllocator.Alloc(length * 4)
	if err != nil {
		panic(err)
	}
	buf := node[:length]
	defer common.DefaultAllocator.Free(node)
	sortedIdx := *(*[]uint32)(unsafe.Pointer(&buf))
	vecs, mapping := task.mergeColumn(sortVecs, &sortedIdx, true, rows, to, schema.HasSortKey())
	// logutil.Infof("mapping is %v", mapping)
	// logutil.Infof("sortedIdx is %v", sortedIdx)
	length = 0
	var blk handle.Block
	toAddr := make([]uint32, 0, len(vecs))
	// index meta for every created block
	// Prepare new block placeholder
	// Build and flush block index if sort key is defined
	// Flush sort key it correlates to only one column
	batchs := make([]*containers.Batch, 0)
	blockHandles := make([]handle.Block, 0)
	for i, vec := range vecs {
		toAddr = append(toAddr, uint32(length))
		length += vec.Length()
		blk, err = toSegEntry.CreateNonAppendableBlock(
			new(objectio.CreateBlockOpt).WithFileIdx(0).WithBlkIdx(uint16(i)))
		if err != nil {
			return err
		}
		task.createdBlks = append(task.createdBlks, blk.GetMeta().(*catalog.BlockEntry))
		blockHandles = append(blockHandles, blk)
		batch := containers.NewBatch()
		batchs = append(batchs, batch)
		vec.Close()
	}

	// Build and flush block index if sort key is defined
	// Flush sort key it correlates to only one column

	for _, def := range schema.ColDefs {
		if def.IsPhyAddr() {
			continue
		}
		// Skip
		// PhyAddr column was processed before
		// If only one single sort key, it was processed before
		vecs = vecs[:0]
		for _, block := range task.compacted {
			if view, err = block.GetColumnDataById(def.Idx); err != nil {
				return
			}
			defer view.Close()
			view.ApplyDeletes()
			vec := view.Orphan()
			if vec.Length() == 0 {
				continue
			}
			defer vec.Close()
			vecs = append(vecs, vec)
		}
		vecs, _ := task.mergeColumn(vecs, &sortedIdx, false, rows, to, schema.HasSortKey())
		for i := range vecs {
			defer vecs[i].Close()
		}
		for i, vec := range vecs {
			batchs[i].AddVector(def.Name, vec)
		}
	}

	name := objectio.BuildObjectName(&task.toSegEntry.ID, 0)
	writer, err := blockio.NewBlockWriterNew(task.mergedBlks[0].GetBlockData().GetFs().Service, name, schema.Version, seqnums)
	if err != nil {
		return err
	}
	if schema.HasPK() {
		pkIdx := schema.GetSingleSortKeyIdx()
		writer.SetPrimaryKey(uint16(pkIdx))
	}
	for _, bat := range batchs {
		_, err = writer.WriteBatch(containers.ToCNBatch(bat))
		if err != nil {
			return err
		}
	}
	blocks, _, err := writer.Sync(context.Background())
	if err != nil {
		return err
	}
	var metaLoc objectio.Location
	for i, block := range blocks {
		metaLoc = blockio.EncodeLocation(name, block.GetExtent(), uint32(batchs[i].Length()), block.GetID())
		if err = blockHandles[i].UpdateMetaLoc(metaLoc); err != nil {
			return err
		}
	}
	for _, blk := range task.createdBlks {
		if err = blk.GetBlockData().Init(); err != nil {
			return err
		}
	}

	for _, compacted := range task.compacted {
		seg := compacted.GetSegment()
		if err = seg.SoftDeleteBlock(compacted.ID()); err != nil {
			return err
		}
	}
	for _, entry := range task.mergedSegs {
		if err = task.rel.SoftDeleteSegment(&entry.ID); err != nil {
			return err
		}
	}

	table := task.toSegEntry.GetTable()
	txnEntry := txnentries.NewMergeBlocksEntry(
		task.txn,
		task.rel,
		task.mergedSegs,
		task.createdSegs,
		task.mergedBlks,
		task.createdBlks,
		mapping,
		fromAddr,
		toAddr,
		task.deletes,
		skipBlks,
		task.scheduler)
	if err = task.txn.LogTxnEntry(table.GetDB().ID, table.ID, txnEntry, ids); err != nil {
		return err
	}

	logutil.Info("[Done] Mergeblocks",
		common.AnyField("txn-start-ts", task.txn.GetStartTS().ToString()),
		common.OperationField(task.Name()),
		common.OperandField(task),
		common.DurationField(time.Since(now)))
	return err
}
