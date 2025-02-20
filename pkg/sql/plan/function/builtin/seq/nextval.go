// Copyright 2021 - 2022 Matrix Origin
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

package seq

import (
	"fmt"
	"math"

	"github.com/matrixorigin/matrixone/pkg/catalog"
	"github.com/matrixorigin/matrixone/pkg/common/moerr"
	"github.com/matrixorigin/matrixone/pkg/container/nulls"
	"github.com/matrixorigin/matrixone/pkg/container/types"
	"github.com/matrixorigin/matrixone/pkg/container/vector"
	"github.com/matrixorigin/matrixone/pkg/defines"
	"github.com/matrixorigin/matrixone/pkg/txn/client"
	"github.com/matrixorigin/matrixone/pkg/vm/engine"
	"github.com/matrixorigin/matrixone/pkg/vm/process"
	"golang.org/x/exp/constraints"
)

// TODO: Requires usage or update privilege on the sequence.
var setEdge = true

// Retrieve values of this sequence.
// Set curval,lastval of current session.
// Set is_called to true if it is false, if is_called is true Advance last_seq_num.
// Return advanced last_seq_num.
func Nextval(vecs []*vector.Vector, proc *process.Process) (*vector.Vector, error) {
	e := proc.Ctx.Value(defines.EngineKey{}).(engine.Engine)

	txn := proc.TxnOperator
	if txn == nil {
		return nil, moerr.NewInternalError(proc.Ctx, "Nextval: txn operator is nil")
	}
	// nextval is the real implementation of nextval function.
	tblnames := vector.MustStrCol(vecs[0])
	restrings := make([]string, len(tblnames))
	isNulls := make([]bool, len(tblnames))

	res, err := proc.AllocVectorOfRows(types.T_varchar.ToType(), 0, nil)
	if err != nil {
		return nil, err
	}

	for i := 0; i < vecs[0].Length(); i++ {
		if nulls.Contains(vecs[0].GetNulls(), uint64(i)) {
			isNulls[i] = true
			continue
		}
		s, err := nextval(tblnames[i], proc, e, txn)
		if err != nil {
			return nil, err
		}
		restrings[i] = s
	}

	vector.AppendStringList(res, restrings, isNulls, proc.Mp())

	// Set last val.
	for i := len(restrings) - 1; i >= 0; i-- {
		if restrings[i] != "" {
			proc.SessionInfo.SeqLastValue[0] = restrings[i]
			break
		}
	}
	return res, nil
}

func nextval(tblname string, proc *process.Process, e engine.Engine, txn client.TxnOperator) (string, error) {
	db := proc.SessionInfo.Database
	dbHandler, err := e.Database(proc.Ctx, db, txn)
	if err != nil {
		return "", err
	}
	rel, err := dbHandler.Relation(proc.Ctx, tblname)
	if err != nil {
		return "", err
	}

	// Check is sequence table.
	td, err := rel.TableDefs(proc.Ctx)
	if err != nil {
		return "", err
	}
	if td[len(td)-1].(*engine.PropertiesDef).Properties[0].Value != catalog.SystemSequenceRel {
		return "", moerr.NewInternalError(proc.Ctx, "Table input is not a sequence")
	}

	values, err := proc.SessionInfo.SqlHelper.ExecSql(fmt.Sprintf("select * from `%s`.`%s`", db, tblname))
	if err != nil {
		return "", err
	}
	if values == nil {
		return "", moerr.NewInternalError(proc.Ctx, "Failed to get sequence meta data.")
	}

	switch values[0].(type) {
	case int16:
		// Get values store in sequence table.
		lsn, minv, maxv, _, incrv, cycle, isCalled := values[0].(int16), values[1].(int16), values[2].(int16),
			values[3].(int16), values[4].(int64), values[5].(bool), values[6].(bool)
		// When iscalled is not set, set it and do not advance sequence number.
		if !isCalled {
			return setIsCalled(proc, rel, lsn, db, tblname)
		}
		// When incr is over the range of this datatype.
		if incrv > math.MaxInt16 || incrv < math.MinInt16 {
			if cycle {
				return advanceSeq(lsn, minv, maxv, int16(incrv), cycle, incrv < 0, setEdge, rel, proc, db, tblname)
			} else {
				return "", moerr.NewInternalError(proc.Ctx, "Reached maximum value of sequence %s", tblname)
			}
		}
		// Tranforming incrv to this datatype and make it positive for generic use.
		return advanceSeq(lsn, minv, maxv, makePosIncr[int16](incrv), cycle, incrv < 0, !setEdge, rel, proc, db, tblname)
	case int32:
		lsn, minv, maxv, _, incrv, cycle, isCalled := values[0].(int32), values[1].(int32), values[2].(int32),
			values[3].(int32), values[4].(int64), values[5].(bool), values[6].(bool)
		if !isCalled {
			return setIsCalled(proc, rel, lsn, db, tblname)
		}
		if incrv > math.MaxInt64 || incrv < math.MinInt64 {
			if cycle {
				return advanceSeq(lsn, minv, maxv, int32(incrv), cycle, incrv < 0, setEdge, rel, proc, db, tblname)
			} else {
				return "", moerr.NewInternalError(proc.Ctx, "Reached maximum value of sequence %s", tblname)
			}
		}
		return advanceSeq(lsn, minv, maxv, makePosIncr[int32](incrv), cycle, incrv < 0, !setEdge, rel, proc, db, tblname)
	case int64:
		lsn, minv, maxv, _, incrv, cycle, isCalled := values[0].(int64), values[1].(int64), values[2].(int64),
			values[3].(int64), values[4].(int64), values[5].(bool), values[6].(bool)
		if !isCalled {
			return setIsCalled(proc, rel, lsn, db, tblname)
		}
		return advanceSeq(lsn, minv, maxv, makePosIncr[int64](incrv), cycle, incrv < 0, !setEdge, rel, proc, db, tblname)
	case uint16:
		lsn, minv, maxv, _, incrv, cycle, isCalled := values[0].(uint16), values[1].(uint16), values[2].(uint16),
			values[3].(uint16), values[4].(int64), values[5].(bool), values[6].(bool)
		if !isCalled {
			return setIsCalled(proc, rel, lsn, db, tblname)
		}
		if incrv > math.MaxUint16 || -incrv > math.MaxUint16 {
			if cycle {
				return advanceSeq(lsn, minv, maxv, uint16(incrv), cycle, incrv < 0, setEdge, rel, proc, db, tblname)
			} else {
				return "", moerr.NewInternalError(proc.Ctx, "Reached maximum value of sequence %s", tblname)
			}
		}
		return advanceSeq(lsn, minv, maxv, makePosIncr[uint16](incrv), cycle, incrv < 0, !setEdge, rel, proc, db, tblname)
	case uint32:
		lsn, minv, maxv, _, incrv, cycle, isCalled := values[0].(uint32), values[1].(uint32), values[2].(uint32),
			values[3].(uint32), values[4].(int64), values[5].(bool), values[6].(bool)
		if !isCalled {
			return setIsCalled(proc, rel, lsn, db, tblname)
		}
		if incrv > math.MaxUint32 || -incrv > math.MaxUint32 {
			if cycle {
				return advanceSeq(lsn, minv, maxv, uint32(incrv), cycle, incrv < 0, setEdge, rel, proc, db, tblname)
			} else {
				return "", moerr.NewInternalError(proc.Ctx, "Reached maximum value of sequence %s", tblname)
			}
		}
		return advanceSeq(lsn, minv, maxv, makePosIncr[uint32](incrv), cycle, incrv < 0, !setEdge, rel, proc, db, tblname)
	case uint64:
		lsn, minv, maxv, _, incrv, cycle, isCalled := values[0].(uint64), values[1].(uint64), values[2].(uint64),
			values[3].(uint64), values[4].(int64), values[5].(bool), values[6].(bool)
		if !isCalled {
			return setIsCalled(proc, rel, lsn, db, tblname)
		}
		return advanceSeq(lsn, minv, maxv, makePosIncr[uint64](incrv), cycle, incrv < 0, !setEdge, rel, proc, db, tblname)
	}

	return "", moerr.NewInternalError(proc.Ctx, "Wrong types of sequence number or failed to read the sequence table")
}

func makePosIncr[T constraints.Integer](incr int64) T {
	if incr < 0 {
		return T(-incr)
	}
	return T(incr)
}

func advanceSeq[T constraints.Integer](lsn, minv, maxv, incrv T,
	cycle, minus, setEdge bool, rel engine.Relation, proc *process.Process, db, tblname string) (string, error) {
	if setEdge {
		// Set lastseqnum to maxv when this is a descending sequence.
		if minus {
			return setSeq(proc, maxv, rel, db, tblname)
		}
		// Set lastseqnum to minv
		return setSeq(proc, minv, rel, db, tblname)
	}
	var adseq T
	if minus {
		adseq = lsn - incrv
	} else {
		adseq = lsn + incrv
	}

	// check descending sequence and reach edge
	if minus && (adseq < minv || adseq > lsn) {
		if !cycle {
			return "", moerr.NewInternalError(proc.Ctx, "Reached maximum value of sequence %s", tblname)
		}
		return setSeq(proc, maxv, rel, db, tblname)
	}

	// checkout ascending sequence and reach edge
	if !minus && (adseq > maxv || adseq < lsn) {
		if !cycle {
			return "", moerr.NewInternalError(proc.Ctx, "Reached maximum value of sequence %s", tblname)
		}
		return setSeq(proc, minv, rel, db, tblname)
	}

	// Otherwise set to adseq.
	return setSeq(proc, adseq, rel, db, tblname)
}

func setSeq[T constraints.Integer](proc *process.Process, setv T, rel engine.Relation, db, tbl string) (string, error) {
	_, err := proc.SessionInfo.SqlHelper.ExecSql(fmt.Sprintf("update `%s`.`%s` set last_seq_num = %d", db, tbl, setv))
	if err != nil {
		return "", err
	}

	tblId := rel.GetTableID(proc.Ctx)
	ress := fmt.Sprint(setv)

	// Set Curvalues here. Add new slot to proc's related field.
	proc.SessionInfo.SeqAddValues[tblId] = ress

	return ress, nil
}

func setIsCalled[T constraints.Integer](proc *process.Process, rel engine.Relation, lsn T, db, tbl string) (string, error) {
	// Set is called to true.
	_, err := proc.SessionInfo.SqlHelper.ExecSql(fmt.Sprintf("update `%s`.`%s` set is_called = true", db, tbl))
	if err != nil {
		return "", err
	}

	tblId := rel.GetTableID(proc.Ctx)
	ress := fmt.Sprint(lsn)

	// Set Curvalues here. Add new slot to proc's related field.
	proc.SessionInfo.SeqAddValues[tblId] = ress

	return ress, nil
}
