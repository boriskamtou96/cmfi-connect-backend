package db

import "context"

// ChooseDiscipleMakerTx makes the authority [id] of [userID] their disciple
// maker; the former one stays among their authorities. sql.ErrNoRows when
// the authority isn't theirs (or was declined).
func (s *SQLStore) ChooseDiscipleMakerTx(ctx context.Context, userID, id int64) (Authority, error) {
	var chosen Authority
	err := s.execTx(ctx, func(q *Queries) error {
		if err := q.ClearDiscipleMaker(ctx, userID); err != nil {
			return err
		}
		var err error
		chosen, err = q.MakeDiscipleMaker(ctx, MakeDiscipleMakerParams{ID: id, UserID: userID})
		return err
	})
	return chosen, err
}
