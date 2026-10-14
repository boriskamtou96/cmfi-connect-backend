package db

import (
	"context"
	"math"
)

// clampPosition keeps a nation's number between 1 and [count].
func clampPosition(position, count int32) int32 {
	if position < 1 || position > count {
		return count
	}
	return position
}

// CreateNationTx adds a nation at [arg.Position] (the end when 0 or too far),
// the following ones moving down by one: everything or nothing.
func (s *SQLStore) CreateNationTx(ctx context.Context, arg CreateNationParams) (MinistryNation, error) {
	var nation MinistryNation
	err := s.execTx(ctx, func(q *Queries) error {
		count, err := q.CountNations(ctx)
		if err != nil {
			return err
		}
		arg.Position = clampPosition(arg.Position, count+1)
		if err := q.ShiftNations(ctx, ShiftNationsParams{Delta: 1, First: arg.Position, Last: math.MaxInt32}); err != nil {
			return err
		}
		nation, err = q.CreateNation(ctx, arg)
		return err
	})
	return nation, err
}

// UpdateNationTx changes a nation; a new number moves the ones in between by
// one, so the numbers stay 1, 2, 3… without gap.
func (s *SQLStore) UpdateNationTx(ctx context.Context, arg UpdateNationParams) (MinistryNation, error) {
	var nation MinistryNation
	err := s.execTx(ctx, func(q *Queries) error {
		current, err := q.GetNation(ctx, arg.ID)
		if err != nil {
			return err
		}
		count, err := q.CountNations(ctx)
		if err != nil {
			return err
		}
		arg.Position = clampPosition(arg.Position, count)
		switch {
		case arg.Position < current.Position:
			err = q.ShiftNations(ctx, ShiftNationsParams{Delta: 1, First: arg.Position, Last: current.Position - 1})
		case arg.Position > current.Position:
			err = q.ShiftNations(ctx, ShiftNationsParams{Delta: -1, First: current.Position + 1, Last: arg.Position})
		}
		if err != nil {
			return err
		}
		nation, err = q.UpdateNation(ctx, arg)
		return err
	})
	return nation, err
}

// DeleteNationTx removes a nation; the following ones move up by one.
func (s *SQLStore) DeleteNationTx(ctx context.Context, id int64) error {
	return s.execTx(ctx, func(q *Queries) error {
		current, err := q.GetNation(ctx, id)
		if err != nil {
			return err
		}
		if err := q.DeleteNation(ctx, id); err != nil {
			return err
		}
		return q.ShiftNations(ctx, ShiftNationsParams{Delta: -1, First: current.Position + 1, Last: math.MaxInt32})
	})
}
