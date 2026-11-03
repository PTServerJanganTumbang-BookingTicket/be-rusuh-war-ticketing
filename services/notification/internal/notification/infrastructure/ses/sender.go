// Package ses sends email through Amazon SES v2.
package ses

import (
	"context"
	"fmt"

	awsses "github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/port/outbound"
)

// API is the slice of the SES client this adapter uses, so tests can
// substitute it without the AWS SDK.
type API interface {
	SendEmail(ctx context.Context, in *awsses.SendEmailInput, opts ...func(*awsses.Options)) (*awsses.SendEmailOutput, error)
}

// Sender is the SES-backed IEmailSender.
type Sender struct {
	client           API
	fromAddress      string
	configurationSet string
}

// NewSender returns the SES adapter.
//
// configurationSet is what routes bounces and complaints back to SNS. Without
// one, a bouncing address is invisible and SES eventually throttles the whole
// account for poor reputation.
func NewSender(client API, fromAddress, configurationSet string) portout.IEmailSender {
	return &Sender{client: client, fromAddress: fromAddress, configurationSet: configurationSet}
}

// Send delivers one message.
//
// Both body parts are supplied: a multipart/alternative message lets the
// client pick, and a text part is what makes the mail readable in clients that
// strip HTML and less likely to be scored as spam.
func (s *Sender) Send(ctx context.Context, msg entity.Message) error {
	in := &awsses.SendEmailInput{
		FromEmailAddress: &s.fromAddress,
		Destination:      &types.Destination{ToAddresses: []string{msg.To}},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: &msg.Subject},
				Body: &types.Body{
					Html: &types.Content{Data: &msg.HTMLBody},
					Text: &types.Content{Data: &msg.TextBody},
				},
			},
		},
	}
	if s.configurationSet != "" {
		in.ConfigurationSetName = &s.configurationSet
	}

	if _, err := s.client.SendEmail(ctx, in); err != nil {
		return fmt.Errorf("ses: send: %w", err)
	}
	return nil
}
