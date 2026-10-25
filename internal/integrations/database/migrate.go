// internal/database/migrate.go

package database

import (
	"fmt"

	"backend/internal/models"
	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
	if err := migrateLegacyStocks(db); err != nil {
		return err
	}

	if err := db.AutoMigrate(
		&models.User{},
		&models.Stock{},
		&models.Position{},
		&models.Session{},
		&models.OAuthState{},
		&models.Transaction{},
		&models.Order{},
		&models.Watchlist{},
		&models.WatchlistItem{},
	); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	// AutoMigrate retypes columns whose type differs (users.balance and positions.* move from
	// numeric(15,x) to numeric(18,x), NOT NULL included), but it does not add NOT NULL to a column
	// whose type already matches, which is the case for the orderbook_id column added above.
	// SET NOT NULL is a no-op when the column already has it.
	if err := db.Exec(`ALTER TABLE stocks ALTER COLUMN orderbook_id SET NOT NULL`).Error; err != nil {
		return fmt.Errorf("stocks.orderbook_id not null: %w", err)
	}

	return nil
}

// migrateLegacyStocks prepares a pre-NW-04 stocks table for AutoMigrate. Before NW-04 stocks were
// keyed by a unique symbol and had no orderbook_id. AutoMigrate alone would try to add orderbook_id
// as NOT NULL with a unique index, which fails on existing rows, and it never drops the old unique
// index. So, idempotently: add the column as nullable, backfill it with the symbol (the old natural
// key, unique by that index), and drop the unique symbol index. AutoMigrate then creates
// idx_stocks_orderbook_id and idx_stocks_symbol_lookup and widens the money and quantity columns to
// numeric(18,2)/numeric(18,4) from the model tags; Migrate sets orderbook_id NOT NULL afterwards.
// Fresh databases skip this.
func migrateLegacyStocks(db *gorm.DB) error {
	if !db.Migrator().HasTable(&models.Stock{}) {
		return nil
	}
	statements := []string{
		`ALTER TABLE stocks ADD COLUMN IF NOT EXISTS orderbook_id text`,
		`UPDATE stocks SET orderbook_id = symbol WHERE orderbook_id IS NULL OR orderbook_id = ''`,
		`DROP INDEX IF EXISTS idx_stocks_symbol`,
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("migrate legacy stocks (%s): %w", stmt, err)
		}
	}
	return nil
}
