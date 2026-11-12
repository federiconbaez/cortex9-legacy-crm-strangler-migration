// Package store abre la base SQLite y cuenta las consultas que se ejecutan.
//
// El contador existe para medir el problema N+1: cada llamada a Query,
// QueryRow o Exec (directa o dentro de una transacción) suma uno. No es un
// profiler; es una métrica barata y exacta cuando se mide en serie.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"

	_ "modernc.org/sqlite" // driver SQLite en Go puro (sin cgo)
)

// DB envuelve *sql.DB y cuenta consultas.
type DB struct {
	raw     *sql.DB
	queries atomic.Int64
}

// Open abre (o crea) la base en path. Usa WAL para permitir lecturas
// concurrentes mientras hay una escritura en curso.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	raw.SetMaxOpenConns(8)
	raw.SetMaxIdleConns(8)
	if err := raw.Ping(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return &DB{raw: raw}, nil
}

// Close cierra la base.
func (d *DB) Close() error { return d.raw.Close() }

// Queries devuelve el total acumulado de consultas ejecutadas.
func (d *DB) Queries() int64 { return d.queries.Load() }

// Query ejecuta una consulta que devuelve filas.
func (d *DB) Query(q string, args ...any) (*sql.Rows, error) {
	return d.QueryContext(context.Background(), q, args...)
}

// QueryContext ejecuta una consulta que devuelve filas.
func (d *DB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.queries.Add(1)
	return d.raw.QueryContext(ctx, q, args...)
}

// QueryRow ejecuta una consulta que devuelve a lo sumo una fila.
func (d *DB) QueryRow(q string, args ...any) *sql.Row {
	return d.QueryRowContext(context.Background(), q, args...)
}

// QueryRowContext ejecuta una consulta que devuelve a lo sumo una fila.
func (d *DB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	d.queries.Add(1)
	return d.raw.QueryRowContext(ctx, q, args...)
}

// Exec ejecuta una sentencia sin filas.
func (d *DB) Exec(q string, args ...any) (sql.Result, error) {
	return d.ExecContext(context.Background(), q, args...)
}

// ExecContext ejecuta una sentencia sin filas.
func (d *DB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	d.queries.Add(1)
	return d.raw.ExecContext(ctx, q, args...)
}

// Tx es una transacción que también cuenta consultas.
type Tx struct {
	tx *sql.Tx
	db *DB
}

// BeginTx abre una transacción.
func (d *DB) BeginTx(ctx context.Context) (*Tx, error) {
	tx, err := d.raw.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx, db: d}, nil
}

// ExecContext ejecuta una sentencia dentro de la transacción.
func (t *Tx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	t.db.queries.Add(1)
	return t.tx.ExecContext(ctx, q, args...)
}

// QueryRowContext consulta una fila dentro de la transacción.
func (t *Tx) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	t.db.queries.Add(1)
	return t.tx.QueryRowContext(ctx, q, args...)
}

// Commit confirma la transacción.
func (t *Tx) Commit() error { return t.tx.Commit() }

// Rollback deshace la transacción.
func (t *Tx) Rollback() error { return t.tx.Rollback() }
