package db

import "context"

// AnnouncementAttachmentInput is an image or a PDF of an announcement.
type AnnouncementAttachmentInput struct {
	MediaID int64
	Name    string
}

// CreateAnnouncementTx saves the announcement with its files and its
// recipients ([userIDs]): everything is saved or nothing is.
func (s *SQLStore) CreateAnnouncementTx(ctx context.Context, arg CreateAnnouncementParams, attachments []AnnouncementAttachmentInput, userIDs []int64) (Announcement, error) {
	var announcement Announcement
	err := s.execTx(ctx, func(q *Queries) error {
		var err error
		announcement, err = q.CreateAnnouncement(ctx, arg)
		if err != nil {
			return err
		}
		for i, attachment := range attachments {
			if err := q.CreateAnnouncementAttachment(ctx, CreateAnnouncementAttachmentParams{
				AnnouncementID: announcement.ID,
				Position:       int32(i + 1),
				MediaID:        attachment.MediaID,
				Name:           attachment.Name,
			}); err != nil {
				return err
			}
		}
		return q.AddAnnouncementRecipients(ctx, AddAnnouncementRecipientsParams{
			AnnouncementID: announcement.ID,
			UserIds:        userIDs,
		})
	})
	return announcement, err
}
