// Package components is the API's handle on the component tables (Epic 51): the shared
// store (shared/components/store) stamped with this build's version. Desired state is
// written only through the API (an admin's change, audited in the same transaction);
// every runner reads it and reports its own status rows.
package components

import (
	"database/sql"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// New is the store on db, its status rows stamped with this build's version.
func New(db *sql.DB) *store.Store {
	s := store.New(db)
	s.Version = version.Version
	return s
}
