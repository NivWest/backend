// internal/database/migrate.go

package database

import (
    "fmt"

    "backend/internal/models"
    "gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
    if err := db.AutoMigrate(
        &models.User{},
        &models.Stock{},
        &models.Position{},
    ); err != nil {
        return fmt.Errorf("auto migrate: %w", err)
    }

    return nil
}