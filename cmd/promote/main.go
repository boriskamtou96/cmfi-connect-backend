// Command promote gives a member a role. It names the first superadmin, who
// then manages the admins from the app (PUT /v1/admin/members/:username/role).
//
//	go run ./cmd/promote -phone +237690000001                // superadmin
//	go run ./cmd/promote -username boris.kamtou -role admin
//	go run ./cmd/promote -username boris.kamtou -role user   // takes the role back
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/db"
	dbs "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/usernames"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
)

func main() {
	phone := flag.String("phone", "", "phone number of the member, as stored (+237...)")
	username := flag.String("username", "", "pseudo of the member")
	role := flag.String("role", "superadmin", "user, admin or superadmin")
	flag.Parse()

	if (*phone == "") == (*username == "") || !slices.Contains([]string{"user", "admin", "superadmin"}, *role) {
		fmt.Fprintln(os.Stderr, "usage: promote (-phone +237... | -username pseudo) [-role user|admin|superadmin]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	cfg, err := utils.LoadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	conn := db.New(cfg.DB)
	defer conn.Close()
	store := dbs.NewSQLStore(conn)
	ctx := context.Background()

	var member dbs.User
	if *phone != "" {
		member, err = store.GetByPhoneNumber(ctx, *phone)
	} else {
		member, err = store.GetUserByUsername(ctx, usernames.Normalize(*username))
	}
	if errors.Is(err, sql.ErrNoRows) {
		log.Fatal("no member with this phone number or pseudo")
	}
	if err != nil {
		log.Fatal(err)
	}

	updated, err := store.UpdateUserRole(ctx, dbs.UpdateUserRoleParams{ID: member.ID, Role: *role})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("@%s (%s): %s -> %s\n", updated.Username, updated.PhoneNumber, member.Role, updated.Role)
}
