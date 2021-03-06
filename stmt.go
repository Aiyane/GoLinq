package golinq

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// FakeStmt  is implementation of Stmt sql interfcae
type FakeStmt struct {
	connection   *FakeConn
	q            string    // SQL 语句
	command      string    // SELECT, INSERT, UPDATE, DELETE
	next         *FakeStmt // used for returning multiple results.
	closed       bool      // If connection closed already
	colName      []string  // Names of columns in response
	colType      []string  // Not used for now
	placeholders int       // Amount of passed args
}

// ColumnConverter returns a ValueConverter for the provided
// column index.
func (s *FakeStmt) ColumnConverter(idx int) driver.ValueConverter {
	return driver.DefaultParameterConverter
}

// Close closes the connection
func (s *FakeStmt) Close() error {
	// No connection added
	if s.connection == nil {
		panic("nil conn in FakeStmt.Close")
	}
	if s.connection.db == nil {
		panic("in FakeStmt.Close, conn's db is nil (already closed)")
	}
	if !s.closed {
		s.closed = true
	}
	if s.next != nil {
		s.next.Close()
	}
	return nil
}

var errClosed = errors.New("fake_db_driver: statement has been closed")

// Exec executes a query that doesn't return rows, such
// as an INSERT or UPDATE.
//
// Deprecated: Drivers should implement StmtExecContext instead (or additionally).
func (s *FakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	panic("Using ExecContext")
}

func valueToSqlValue(v interface{}) string {
	switch val := v.(type) {
	case time.Time:
		return fmt.Sprintf("\"%v\"", val)
	case string:
		return fmt.Sprintf("\"%v\"", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// ExecContext executes a query that doesn't return rows, such
// as an INSERT or UPDATE.
func (s *FakeStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if s.closed {
		return nil, errClosed
	}

	s.q = strings.Replace(s.q, "\"", "", -1)
	if len(args) > 0 {
		// Replace all "?" to "%v" and replace them with the values after
		for i := 0; i < len(args); i++ {
			s.q = strings.Replace(s.q, "?", "%v", 1)
			s.q = fmt.Sprintf(s.q, valueToSqlValue(args[i].Value))
		}
	}

	res := SqlRun(s.q, DB2DataBase[s.connection.db.name], "").(int)
	fResp := &FakeResponse{
		Response:     make([]map[string]interface{}, 0),
		Exceptions:   &Exceptions{},
		RowsAffected: int64(res),
		LastInsertID: int64(res),
	}

	// To emulate any exception during query which returns rows
	if fResp.Exceptions != nil && fResp.Exceptions.HookExecBadConnection != nil && fResp.Exceptions.HookExecBadConnection() {
		return nil, driver.ErrBadConn
	}

	if fResp.Error != nil {
		return nil, fResp.Error
	}

	if fResp.Callback != nil {
		fResp.Callback(s.q, args)
	}

	switch s.command {
	case "INSERT":
		id := fResp.LastInsertID
		if id == 0 {
			id = rand.Int63()
		}
		res := NewFakeResult(id, 1)
		return res, nil
	case "UPDATE":
		return driver.RowsAffected(fResp.RowsAffected), nil
	case "DELETE":
		return driver.RowsAffected(fResp.RowsAffected), nil
	}
	return nil, fmt.Errorf("unimplemented statement Exec command type of %q", s.command)
}

// 废弃了
func (s *FakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	panic("Use QueryContext")
}

// QueryContext executes a query that may return rows, such as a
// SELECT.
func (s *FakeStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {

	if s.closed {
		return nil, errClosed
	}

	s.q = strings.Replace(s.q, "\"", "", -1)
	if len(args) > 0 {
		// Replace all "?" to "%v" and replace them with the values after
		for i := 0; i < len(args); i++ {
			s.q = strings.Replace(s.q, "?", "%v", 1)
			s.q = fmt.Sprintf(s.q, valueToSqlValue(args[i].Value))
		}
	}

	records := SqlRun(s.q, DB2DataBase[s.connection.db.name], "")
	var resp []map[string]interface{}
	if records == nil {
		resp = make([]map[string]interface{}, 0, 0)
	} else {
		resp = make([]map[string]interface{}, 0, len(records.([]interface{})))
		for _, record := range records.([]interface{}) {
			resp = append(resp, record.(map[string]interface{}))
		}
	}
	fResp := &FakeResponse{
		Response:   resp,
		Exceptions: &Exceptions{},
	}
	//fResp := Catcher.FindResponse(s.q, args)

	// 看有没有钩子让你失败
	if fResp.Exceptions != nil && fResp.Exceptions.HookQueryBadConnection != nil && fResp.Exceptions.HookQueryBadConnection() {
		return nil, driver.ErrBadConn
	}

	if fResp.Error != nil {
		return nil, fResp.Error
	}

	resultRows := make([][]*row, 0, 1)
	columnNames := make([]string, 0, 1)
	columnTypes := make([][]string, 0, 1)
	rows := []*row{}

	// Check if we have such query in the map
	colIndexes := make(map[string]int)

	// 把列名以及列名的位置保存起来
	if len(fResp.Response) > 0 {
		for colName := range fResp.Response[0] {
			colIndexes[colName] = len(columnNames)
			columnNames = append(columnNames, colName)
		}
	}

	// Extracting values from result according columns
	for _, record := range fResp.Response {
		oneRow := &row{cols: make([]interface{}, len(columnNames))}
		for _, col := range columnNames {
			oneRow.cols[colIndexes[col]] = record[col]
		}
		rows = append(rows, oneRow)
	}
	resultRows = append(resultRows, rows)

	cursor := &RowsCursor{
		posRow:  -1,
		rows:    resultRows,
		cols:    columnNames,
		colType: columnTypes, // TODO: implement support of that
		errPos:  -1,
		closed:  false,
	}

	if fResp.Callback != nil {
		fResp.Callback(s.q, args)
	}

	return cursor, nil
}

// NumInput returns the number of placeholder parameters.
func (s *FakeStmt) NumInput() int {
	return s.placeholders
}

// FakeTx implements Tx interface
type FakeTx struct {
	c *FakeConn
}

// HookBadCommit is a hook to simulate broken connections
var HookBadCommit func() bool

// Commit commits the transaction
func (tx *FakeTx) Commit() error {
	tx.c.currTx = nil
	if HookBadCommit != nil && HookBadCommit() {
		return driver.ErrBadConn
	}
	return nil
}

// HookBadRollback is a hook to simulate broken connections
var HookBadRollback func() bool

// Rollback rollbacks the transaction
func (tx *FakeTx) Rollback() error {
	tx.c.currTx = nil
	if HookBadRollback != nil && HookBadRollback() {
		return driver.ErrBadConn
	}
	return nil
}
