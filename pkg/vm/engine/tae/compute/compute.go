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

package compute

import (
	"bytes"

	"github.com/RoaringBitmap/roaring"
	"github.com/matrixorigin/matrixone/pkg/container/types"
	"github.com/matrixorigin/matrixone/pkg/container/vector"
	"github.com/matrixorigin/matrixone/pkg/vm/engine/tae/containers"
)

func ShuffleByDeletes(deleteMask, deletes *roaring.Bitmap) (destDelets *roaring.Bitmap) {
	if deletes == nil || deletes.IsEmpty() {
		return deleteMask
	}
	if deleteMask != nil && !deleteMask.IsEmpty() {
		delIt := deleteMask.Iterator()
		destDelets = roaring.New()
		deleteIt := deletes.Iterator()
		deleteCnt := uint32(0)
		for deleteIt.HasNext() {
			del := deleteIt.Next()
			for delIt.HasNext() {
				row := delIt.PeekNext()
				if row < del {
					destDelets.Add(row - deleteCnt)
					delIt.Next()
				} else if row == del {
					delIt.Next()
				} else {
					break
				}
			}
			deleteCnt++
		}
		for delIt.HasNext() {
			row := delIt.Next()
			destDelets.Add(row - deleteCnt)
		}
	}
	return destDelets
}

func GetOffsetMapBeforeApplyDeletes(deletes *roaring.Bitmap) []uint32 {
	if deletes == nil || deletes.IsEmpty() {
		return nil
	}
	prev := -1
	mapping := make([]uint32, 0)
	it := deletes.Iterator()
	for it.HasNext() {
		delete := it.Next()
		for i := uint32(prev + 1); i < delete; i++ {
			mapping = append(mapping, i)
		}
		prev = int(delete)
	}
	mapping = append(mapping, uint32(prev)+1)
	return mapping
}

func GetOffsetOfBytes(
	data *vector.Vector,
	val []byte,
	skipmask *roaring.Bitmap,
) (offset int, exist bool) {
	start, end := 0, data.Length()-1
	var mid int
	for start <= end {
		mid = (start + end) / 2
		res := bytes.Compare(data.GetBytesAt(mid), val)
		if res > 0 {
			end = mid - 1
		} else if res < 0 {
			start = mid + 1
		} else {
			if skipmask != nil && skipmask.Contains(uint32(mid)) {
				return
			}
			offset = mid
			exist = true
			return
		}
	}
	return

}

func GetOffsetWithFunc[T any](
	vals []T,
	val T,
	compare func(a, b T) int64,
	skipmask *roaring.Bitmap,
) (offset int, exist bool) {
	start, end := 0, len(vals)-1
	var mid int
	for start <= end {
		mid = (start + end) / 2
		res := compare(vals[mid], val)
		if res > 0 {
			end = mid - 1
		} else if res < 0 {
			start = mid + 1
		} else {
			if skipmask != nil && skipmask.Contains(uint32(mid)) {
				return
			}
			offset = mid
			exist = true
			return
		}
	}
	return
}

func GetOffsetOfOrdered[T types.OrderedT](vs, v any, skipmask *roaring.Bitmap) (offset int, exist bool) {
	return GetOffsetOfOrdered2[T](vs.([]T), v.(T), skipmask)
}

func GetOffsetOfOrdered2[T types.OrderedT](column []T, val T, skipmask *roaring.Bitmap) (offset int, exist bool) {
	start, end := 0, len(column)-1
	var mid int
	for start <= end {
		mid = (start + end) / 2
		if column[mid] > val {
			end = mid - 1
		} else if column[mid] < val {
			start = mid + 1
		} else {
			if skipmask != nil && skipmask.Contains(uint32(mid)) {
				return
			}
			offset = mid
			exist = true
			return
		}
	}
	return
}

func GetOffsetByVal(data containers.Vector, v any, skipmask *roaring.Bitmap) (offset int, exist bool) {
	switch data.GetType().Oid {
	case types.T_bool:
		return GetOffsetWithFunc(data.Slice().([]bool), v.(bool), CompareBool, skipmask)
	case types.T_int8:
		return GetOffsetOfOrdered[int8](data.Slice(), v, skipmask)
	case types.T_int16:
		return GetOffsetOfOrdered[int16](data.Slice(), v, skipmask)
	case types.T_int32:
		return GetOffsetOfOrdered[int32](data.Slice(), v, skipmask)
	case types.T_int64:
		return GetOffsetOfOrdered[int64](data.Slice(), v, skipmask)
	case types.T_uint8:
		return GetOffsetOfOrdered[uint8](data.Slice(), v, skipmask)
	case types.T_uint16:
		return GetOffsetOfOrdered[uint16](data.Slice(), v, skipmask)
	case types.T_uint32:
		return GetOffsetOfOrdered[uint32](data.Slice(), v, skipmask)
	case types.T_uint64:
		return GetOffsetOfOrdered[uint64](data.Slice(), v, skipmask)
	case types.T_float32:
		return GetOffsetOfOrdered[float32](data.Slice(), v, skipmask)
	case types.T_float64:
		return GetOffsetOfOrdered[float64](data.Slice(), v, skipmask)
	case types.T_date:
		return GetOffsetOfOrdered[types.Date](data.Slice(), v, skipmask)
	case types.T_time:
		return GetOffsetOfOrdered[types.Time](data.Slice(), v, skipmask)
	case types.T_datetime:
		return GetOffsetOfOrdered[types.Datetime](data.Slice(), v, skipmask)
	case types.T_timestamp:
		return GetOffsetOfOrdered[types.Timestamp](data.Slice(), v, skipmask)
	case types.T_decimal64:
		return GetOffsetWithFunc(
			data.Slice().([]types.Decimal64),
			v.(types.Decimal64),
			types.CompareDecimal64,
			skipmask)
	case types.T_decimal128:
		return GetOffsetWithFunc(
			data.Slice().([]types.Decimal128),
			v.(types.Decimal128),
			types.CompareDecimal128,
			skipmask)
	case types.T_TS:
		return GetOffsetWithFunc(
			data.Slice().([]types.TS),
			v.(types.TS),
			types.CompareTSTSAligned,
			skipmask)
	case types.T_Rowid:
		return GetOffsetWithFunc(
			data.Slice().([]types.Rowid),
			v.(types.Rowid),
			types.CompareRowidRowidAligned,
			skipmask)
	case types.T_Blockid:
		return GetOffsetWithFunc(
			data.Slice().([]types.Blockid),
			v.(types.Blockid),
			types.CompareBlockidBlockidAligned,
			skipmask)
	case types.T_uuid:
		return GetOffsetWithFunc(
			data.Slice().([]types.Uuid),
			v.(types.Uuid),
			types.CompareUuid,
			skipmask)
	case types.T_char, types.T_varchar, types.T_blob,
		types.T_binary, types.T_varbinary, types.T_json, types.T_text:
		// column := data.Slice().(*containers.Bytes)
		val := v.([]byte)
		start, end := 0, data.Length()-1
		var mid int
		for start <= end {
			mid = (start + end) / 2
			res := bytes.Compare(data.ShallowGet(mid).([]byte), val)
			if res > 0 {
				end = mid - 1
			} else if res < 0 {
				start = mid + 1
			} else {
				if skipmask != nil && skipmask.Contains(uint32(mid)) {
					return
				}
				offset = mid
				exist = true
				return
			}
		}
		return
	default:
		panic("unsupported type")
	}
}

func GetOrderedMinAndMax[T types.OrderedT](vs ...T) (minv, maxv T) {
	minv = vs[0]
	maxv = vs[0]
	for _, v := range vs[1:] {
		if v < minv {
			minv = v
		}
		if v > maxv {
			maxv = v
		}
	}
	return
}

func GetDecimal64MinAndMax(vs []types.Decimal64) (minv, maxv types.Decimal64) {
	minv = vs[0]
	maxv = vs[0]
	for _, v := range vs[1:] {
		if types.CompareDecimal64(v, minv) < 0 {
			minv = v
		}
		if types.CompareDecimal64(v, maxv) > 0 {
			maxv = v
		}
	}
	return
}

func GetDecimal128MinAndMax(vs []types.Decimal128) (minv, maxv types.Decimal128) {
	minv = vs[0]
	maxv = vs[0]
	for _, v := range vs[1:] {
		if types.CompareDecimal128(v, minv) < 0 {
			minv = v
		}
		if types.CompareDecimal128(v, maxv) > 0 {
			maxv = v
		}
	}
	return
}
