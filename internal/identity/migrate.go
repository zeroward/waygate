package identity

import (
	"context"
	"errors"
	"log/slog"

	"github.com/zeroward/waygate/internal/account"
)

func (s *Service) MigrateFromAC(ctx context.Context, log *slog.Logger) error {
	if s.ac == nil {
		return nil
	}
	const per = 100
	offset := 0
	for {
		rows, total, err := s.ac.ListAccounts(ctx, account.ListFilter{IncludeBots: true, Limit: per, Offset: offset})
		if err != nil {
			return err
		}
		for _, row := range rows {
			acc := &account.Account{ID: row.ID, Username: row.Username, Email: row.Email, GMLevel: row.GMLevel}
			if _, err := s.EnsureFromAC(ctx, acc); err != nil && !errors.Is(err, ErrTaken) && !errors.Is(err, ErrLinkTaken) {
				if log != nil {
					log.Error("identity migrate", "user", row.Username, "err", err)
				}
			}
		}
		offset += per
		if offset >= total || len(rows) == 0 {
			break
		}
	}
	if err := s.store.RemapTickets(ctx); err != nil {
		return err
	}
	return nil
}
