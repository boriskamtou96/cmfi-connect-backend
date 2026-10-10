// Command demo-events adds two editions of a yearly conference, to see the
// events in the app: 2025 (over) and 2026 (to come, with its program, news and
// registrations open). The posters are in data/demo. Demo content: names,
// places and numbers are made up.
//
//	go run ./cmd/demo-events     (or make demo-events)
//
// Running it again changes nothing once the 2026 edition exists.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/db"
	dbs "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
)

const (
	title2025 = "Conférence annuelle 2025"
	title2026 = "Conférence annuelle 2026"
	venue     = "Centre de conférences de Mvolyé"
	mapsURL   = "https://www.google.com/maps/search/?api=1&query=Mvoly%C3%A9+Yaound%C3%A9"
)

func main() {
	ctx := context.Background()
	cfg, err := utils.LoadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	conn := db.New(cfg.DB)
	defer conn.Close()
	store := dbs.NewSQLStore(conn)
	files, err := media.NewDiskStore(cfg.Media.Dir)
	if err != nil {
		log.Fatal(err)
	}

	existing, err := store.ListAllEvents(ctx, dbs.ListAllEventsParams{Lim: 500})
	if err != nil {
		log.Fatal(err)
	}
	for _, event := range existing {
		if event.Title == title2026 {
			fmt.Printf("already there: %s (id %d)\n", event.Title, event.ID)
			return
		}
	}

	douala, err := time.LoadLocation("Africa/Douala")
	if err != nil {
		log.Fatal(err)
	}
	at := func(year int, month time.Month, day, hour, minute int) time.Time {
		return time.Date(year, month, day, hour, minute, 0, 0, douala)
	}

	series, err := store.CreateEventSeries(ctx, "Conférence annuelle")
	if err != nil {
		log.Fatal(err)
	}
	seriesID := sql.NullInt64{Int64: series.ID, Valid: true}

	past, err := store.CreateEvent(ctx, dbs.CreateEventParams{
		SeriesID:         seriesID,
		Title:            title2025,
		Theme:            "La prière qui change tout",
		ThemeVerse:       "« Demandez, et l'on vous donnera ; cherchez, et vous trouverez. » Matthieu 7.7",
		Description:      "Trois jours de prière, d'enseignements et de communion fraternelle.",
		StartsAt:         at(2025, time.November, 21, 8, 0),
		EndsAt:           at(2025, time.November, 23, 13, 0),
		TimeZone:         "Africa/Douala",
		Venue:            venue,
		Address:          "Mvolyé",
		City:             "Yaoundé",
		Country:          "Cameroun",
		MapsUrl:          mapsURL,
		PosterID:         poster(ctx, store, files, "data/demo/conference-2025.jpg"),
		RegistrationOpen: true,
		Status:           "published",
	})
	if err != nil {
		log.Fatal(err)
	}
	_, err = store.CreateEventPost(ctx, dbs.CreateEventPostParams{
		EventID: past.ID,
		Body:    "Merci à tous pour ces trois jours de grâce ! Les messages seront bientôt disponibles.",
		Pinned:  true,
	})
	if err != nil {
		log.Fatal(err)
	}

	next, err := store.CreateEvent(ctx, dbs.CreateEventParams{
		SeriesID:   seriesID,
		Title:      title2026,
		Theme:      "Marcher dans l'Esprit",
		ThemeVerse: "« Si nous vivons par l'Esprit, marchons aussi par l'Esprit. » Galates 5.25",
		Description: "Trois jours pour apprendre ensemble à vivre et à marcher par l'Esprit : " +
			"enseignements, prière, louange et communion fraternelle. Toute la communauté est invitée.",
		StartsAt:             at(2026, time.November, 20, 8, 0),
		EndsAt:               at(2026, time.November, 22, 13, 0),
		TimeZone:             "Africa/Douala",
		Venue:                venue,
		Address:              "Mvolyé",
		City:                 "Yaoundé",
		Country:              "Cameroun",
		MapsUrl:              mapsURL,
		ContactName:          "Secrétariat de la conférence",
		ContactPhone:         "+237690000000",
		PosterID:             poster(ctx, store, files, "data/demo/conference-2026.jpg"),
		RegistrationOpen:     true,
		RegistrationDeadline: sql.NullTime{Time: at(2026, time.November, 15, 23, 59), Valid: true},
		Capacity:             sql.NullInt32{Int32: 1500, Valid: true},
		AsksLodging:          true,
		AsksDepartureCity:    true,
		FeeNote:              "Participation libre. Repas : 2 000 FCFA par jour, à régler sur place.",
		Status:               "published",
	})
	if err != nil {
		log.Fatal(err)
	}

	type hymn struct {
		Book   string `json:"book"`
		Number int    `json:"number"`
	}
	session := func(day, fromH, fromM, toH, toM int, title, speaker, place string, hymns ...hymn) dbs.CreateEventSessionParams {
		if hymns == nil {
			hymns = []hymn{}
		}
		raw, _ := json.Marshal(hymns)
		return dbs.CreateEventSessionParams{
			StartsAt: at(2026, time.November, day, fromH, fromM),
			EndsAt:   sql.NullTime{Time: at(2026, time.November, day, toH, toM), Valid: true},
			Title:    title,
			Speaker:  speaker,
			Place:    place,
			Hymns:    raw,
		}
	}
	_, err = store.ReplaceEventSessionsTx(ctx, next.ID, []dbs.CreateEventSessionParams{
		session(20, 8, 0, 10, 0, "Accueil et inscriptions", "", "Hall principal"),
		session(20, 10, 0, 12, 30, "Culte d'ouverture", "Pasteur principal", "Grande salle",
			hymn{"rendons-ministere-1", 1}, hymn{"rendons-ministere-1", 2}),
		session(20, 15, 0, 17, 0, "Enseignement : « Nés de l'Esprit »", "Pasteur invité", "Grande salle"),
		session(20, 20, 0, 23, 0, "Veillée de prière", "Équipe d'intercession", "Grande salle"),
		session(21, 6, 0, 7, 0, "Prière du matin", "", "Chapelle"),
		session(21, 9, 0, 12, 0, "Enseignement : « Conduits par l'Esprit »", "Pasteur invité", "Grande salle"),
		session(21, 14, 30, 16, 30, "Ateliers : jeunes, couples, ouvriers", "Responsables des ateliers", "Salles annexes"),
		session(21, 19, 0, 22, 0, "Soirée de louange", "Chorale de la conférence", "Grande salle",
			hymn{"rendons-ministere-1", 3}),
		session(22, 9, 0, 12, 0, "Culte de clôture et Sainte Cène", "Pasteur principal", "Grande salle",
			hymn{"rendons-ministere-1", 4}),
		session(22, 12, 0, 13, 0, "Envoi et repas fraternel", "", "Esplanade"),
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, post := range []dbs.CreateEventPostParams{
		{EventID: next.ID, Body: "Le programme complet est en ligne : trois jours pour apprendre à marcher dans l'Esprit."},
		{EventID: next.ID, Pinned: true, Body: "Les inscriptions sont ouvertes ! Inscris-toi avant le 15 novembre " +
			"pour qu'on prévoie ton hébergement et tes repas."},
	} {
		if _, err := store.CreateEventPost(ctx, post); err != nil {
			log.Fatal(err)
		}
	}

	fmt.Printf("%s (id %d) and %s (id %d) added\n", past.Title, past.ID, next.Title, next.ID)
}

// poster stores an image file as the media of an event.
func poster(ctx context.Context, store *dbs.SQLStore, files media.Store, path string) sql.NullInt64 {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	info, err := media.Inspect(data)
	if err != nil {
		log.Fatalf("%s: %v", path, err)
	}
	key := media.NewKey(info.Ext)
	if err := files.Save(key, data); err != nil {
		log.Fatal(err)
	}
	row, err := store.CreateMedia(ctx, dbs.CreateMediaParams{
		Key:         key,
		ContentType: info.ContentType,
		Width:       int32(info.Width),
		Height:      int32(info.Height),
		Color:       info.Color,
		SizeBytes:   int64(len(data)),
	})
	if err != nil {
		log.Fatal(err)
	}
	return sql.NullInt64{Int64: row.ID, Valid: true}
}
