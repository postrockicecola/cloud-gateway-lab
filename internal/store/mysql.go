package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"cloud-gateway-lab/internal/auth"
	"cloud-gateway-lab/internal/endpoint"
)

type MySQL struct {
	db *sql.DB
}

func OpenMySQL(dsn string) (*MySQL, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("mysql ping: %w", err)
	}
	return &MySQL{db: db}, nil
}

func (m *MySQL) Close() error {
	return m.db.Close()
}

func (m *MySQL) LookupKey(ctx context.Context, keyHash string) (auth.Record, error) {
	const q = `
SELECT k.user_id, COALESCE(u.name, ''), k.status, u.status
FROM api_keys k
LEFT JOIN users u ON u.id = k.user_id
WHERE k.key_hash = ?
LIMIT 1`
	var rec auth.Record
	var userStatus sql.NullString
	err := m.db.QueryRowContext(ctx, q, keyHash).Scan(&rec.UserID, &rec.Name, &rec.Status, &userStatus)
	if err == sql.ErrNoRows {
		return auth.Record{}, ErrNotFound
	}
	if err != nil {
		return auth.Record{}, err
	}
	if rec.Status != "active" || (userStatus.Valid && userStatus.String != "" && userStatus.String != "active") {
		rec.Status = "disabled"
	}
	return rec, nil
}

func (m *MySQL) ListEndpoints(ctx context.Context) ([]endpoint.Endpoint, error) {
	const q = `
SELECT id, provider, model, COALESCE(model_name, ''), base_url, COALESCE(api_key, ''),
       weight, COALESCE(region, ''), timeout_ms
FROM endpoints
WHERE status = 'active'`
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []endpoint.Endpoint
	for rows.Next() {
		var ep endpoint.Endpoint
		var timeoutMS int
		if err := rows.Scan(&ep.ID, &ep.Provider, &ep.Model, &ep.ModelName, &ep.BaseURL, &ep.APIKey, &ep.Weight, &ep.Region, &timeoutMS); err != nil {
			return nil, err
		}
		if timeoutMS > 0 {
			ep.Timeout = time.Duration(timeoutMS) * time.Millisecond
		}
		out = append(out, ep)
	}
	return out, rows.Err()
}
