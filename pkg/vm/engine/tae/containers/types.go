// Copyright 2022 Matrix Origin
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package containers

import (
	"io"

	"github.com/RoaringBitmap/roaring"
	"github.com/matrixorigin/matrixone/pkg/common/mpool"
	cnNulls "github.com/matrixorigin/matrixone/pkg/container/nulls"
	"github.com/matrixorigin/matrixone/pkg/container/types"
	cnVector "github.com/matrixorigin/matrixone/pkg/container/vector"
)

type Options struct {
	Capacity  int
	Allocator *mpool.MPool
}

type ItOp = func(v any, isNull bool, row int) error
type ItOpT[T any] func(v T, isNull bool, row int) error

// type ItBytesOp func(v []byte, isNull bool, row int) error

type Vector interface {
	GetType() *types.Type

	// Deep copy ops
	Get(i int) any
	Append(v any, isNull bool)
	CloneWindow(offset, length int, allocator ...*mpool.MPool) Vector

	WriteTo(w io.Writer) (int64, error)
	ReadFrom(r io.Reader) (int64, error)

	// Shallow Ops
	ShallowGet(i int) any
	Window(offset, length int) Vector

	// Deepcopy if const
	TryConvertConst() Vector

	GetDownstreamVector() *cnVector.Vector
	setDownstreamVector(vec *cnVector.Vector)

	Update(i int, v any, isNull bool)
	Compact(*roaring.Bitmap)

	Extend(o Vector)
	ExtendWithOffset(src Vector, srcOff, srcLen int)

	Foreach(op ItOp, sels *roaring.Bitmap) error
	ForeachWindow(offset, length int, op ItOp, sels *roaring.Bitmap) error

	Length() int
	Allocated() int
	GetAllocator() *mpool.MPool

	IsNull(i int) bool
	HasNull() bool
	NullMask() *cnNulls.Nulls
	// NullCount will consider ConstNull and Const vector
	NullCount() int

	Slice() any

	Close()

	// Test functions
	Equals(o Vector) bool
	String() string
	PPString(num int) string
	AppendMany(vs []any, isNulls []bool)
	Delete(i int)
}

type Batch struct {
	Attrs   []string
	Vecs    []Vector
	Deletes *roaring.Bitmap
	Nameidx map[string]int
	// refidx  map[int]int
}

type BatchWithVersion struct {
	*Batch
	Seqnums    []uint16
	NextSeqnum uint16
	Version    uint32
}
