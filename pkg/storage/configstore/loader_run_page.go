package configstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
)

func (s *loaderStore) GetLoaderRunForLoaders(ctx context.Context, loaderIDs []string, runID string) (domain.LoaderRunSummary, error) {
	loaderIDs = normalizedLoaderRunPageIDs(loaderIDs)
	runID = strings.TrimSpace(runID)
	if len(loaderIDs) == 0 {
		return domain.LoaderRunSummary{}, loaderRunPageNotFound(runID, nil)
	}
	placeholders := make([]string, len(loaderIDs))
	args := make([]any, 0, len(loaderIDs)+1)
	args = append(args, runID)
	for index, loaderID := range loaderIDs {
		placeholders[index] = "?"
		args = append(args, loaderID)
	}
	row := s.db.QueryRowContext(ctx, loaders.SelectLoaderRunSQL()+` WHERE run_id = ? AND loader_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY loader_id ASC LIMIT 1`, args...)
	item, err := loaders.ScanLoaderRun(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.LoaderRunSummary{}, loaderRunPageNotFound(runID, err)
		}
		return domain.LoaderRunSummary{}, err
	}
	return item, nil
}

func loaderRunPageNotFound(runID string, cause error) error {
	return domain.ResourceError(domain.ErrNotFound, "loader run", runID, fmt.Sprintf("loader run %s not found", runID), cause)
}

func (s *loaderStore) ListLoaderRunsPage(ctx context.Context, filter loaders.LoaderRunPageFilter) ([]domain.LoaderRunSummary, error) {
	loaderIDs := normalizedLoaderRunPageIDs(filter.LoaderIDs)
	if len(loaderIDs) == 0 {
		return []domain.LoaderRunSummary{}, nil
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	placeholders := make([]string, len(loaderIDs))
	args := make([]any, 0, len(loaderIDs)+7)
	for index, loaderID := range loaderIDs {
		placeholders[index] = "?"
		args = append(args, loaderID)
	}
	query := loaders.SelectLoaderRunSQL() + ` WHERE loader_id IN (` + strings.Join(placeholders, ",") + `)`
	if !filter.BeforeStartedAt.IsZero() {
		query += ` AND (started_at < ? OR (started_at = ? AND (loader_id < ? OR (loader_id = ? AND run_id < ?))))`
		beforeMillis := filter.BeforeStartedAt.UTC().UnixMilli()
		args = append(args, beforeMillis, beforeMillis, strings.TrimSpace(filter.BeforeLoaderID), strings.TrimSpace(filter.BeforeLoaderID), strings.TrimSpace(filter.BeforeRunID))
	}
	query += ` ORDER BY started_at DESC, loader_id DESC, run_id DESC LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query loader run page: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]domain.LoaderRunSummary, 0)
	for rows.Next() {
		item, err := loaders.ScanLoaderRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate loader run page: %w", err)
	}
	return items, nil
}

func normalizedLoaderRunPageIDs(loaderIDs []string) []string {
	seen := make(map[string]struct{}, len(loaderIDs))
	result := make([]string, 0, len(loaderIDs))
	for _, loaderID := range loaderIDs {
		loaderID = strings.TrimSpace(loaderID)
		if loaderID == "" {
			continue
		}
		if _, ok := seen[loaderID]; ok {
			continue
		}
		seen[loaderID] = struct{}{}
		result = append(result, loaderID)
	}
	return result
}
