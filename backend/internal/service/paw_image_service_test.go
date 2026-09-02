package service

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/textproto"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPawImageServiceRejectsUnavailableImageModel(t *testing.T) {
	cfg := NewPawConfigService(
		pawImageConfigGroupsStub{groups: []Group{{ID: 7, Name: "OpenAI", Platform: PlatformOpenAI, Status: StatusActive}}},
		&pawConfigUserSourceStub{user: &User{ID: 42, Username: "user", Email: "user@example.com"}},
		&pawImageConfigChannelStub{channels: map[int64]*Channel{
			7: {ID: 70, Status: StatusActive, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5"}}}},
		}},
		&pawConfigDefaultsStoreStub{},
		&PricingService{pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5": {SupportsReasoning: true},
		}},
	)
	svc := NewPawImageService(cfg, nil)

	_, err := svc.ValidateGeneration(context.Background(), 42, PawImageGenerationRequest{
		GroupID: 7,
		ModelID: "gpt-5",
		Prompt:  "draw a cat",
	})

	require.Error(t, err)
	require.Equal(t, "CONFIG_UNAVAILABLE", infraerrors.Reason(err))
}

func TestPawImageServiceParsesImageEditMultipart(t *testing.T) {
	body, contentType := buildPawImageEditMultipart(t)
	svc := NewPawImageService(nil, nil)

	req, err := svc.ParseEditMultipart(contentType, body)

	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", req.ModelID)
	require.Equal(t, "edit a cat", req.Prompt)
	require.Len(t, req.Images, 1)
	require.Equal(t, "image.png", req.Images[0].Filename)
	require.Equal(t, "image/png", req.Images[0].MIMEType)
	require.NotEmpty(t, req.Images[0].Data)
	require.NotNil(t, req.Mask)
	require.Equal(t, "mask.png", req.Mask.Filename)
}

func buildPawImageEditMultipart(t *testing.T) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "edit a cat"))

	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="image"; filename="image.png"`},
		"Content-Type":        []string{"image/png"},
	})
	require.NoError(t, err)
	_, err = part.Write([]byte{0x89, 'P', 'N', 'G'})
	require.NoError(t, err)

	part, err = writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="mask"; filename="mask.png"`},
		"Content-Type":        []string{"image/png"},
	})
	require.NoError(t, err)
	_, err = part.Write([]byte{0x89, 'P', 'N', 'G'})
	require.NoError(t, err)

	require.NoError(t, writer.Close())
	return buf.Bytes(), writer.FormDataContentType()
}

type pawImageConfigGroupsStub struct {
	groups []Group
}

func (s pawImageConfigGroupsStub) AvailableGroups(context.Context, int64) ([]Group, error) {
	return append([]Group(nil), s.groups...), nil
}

type pawImageConfigChannelStub struct {
	channels map[int64]*Channel
}

func (s *pawImageConfigChannelStub) GetChannelForGroup(_ context.Context, groupID int64) (*Channel, error) {
	if channel, ok := s.channels[groupID]; ok {
		copyChannel := *channel
		return &copyChannel, nil
	}
	return nil, nil
}
