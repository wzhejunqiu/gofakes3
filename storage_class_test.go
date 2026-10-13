package gofakes3_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/johannesboyne/gofakes3"
)

func assertRestoreNone(t *testing.T, restore *string) {
	t.Helper()
	if restore != nil && *restore != "" {
		t.Fatalf("expected no restore header, got %q", *restore)
	}
}

func assertRestoreOngoing(t *testing.T, restore *string) {
	t.Helper()
	if restore == nil || !strings.Contains(*restore, `ongoing-request="true"`) {
		t.Fatalf("expected ongoing restore, got %#v", restore)
	}
}

func assertRestoreAvailable(t *testing.T, restore *string) {
	t.Helper()
	if restore == nil ||
		!strings.Contains(*restore, `ongoing-request="false"`) ||
		!strings.Contains(*restore, `expiry-date="`) {
		t.Fatalf("expected available restore with expiry-date, got %#v", restore)
	}
}

func restoreExpiryTime(t *testing.T, restore string) time.Time {
	t.Helper()
	const marker = `expiry-date="`
	i := strings.Index(restore, marker)
	if i < 0 {
		t.Fatalf("missing expiry-date in %q", restore)
	}
	rest := restore[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("unterminated expiry-date in %q", restore)
	}
	expiry, err := time.Parse("Mon, 02 Jan 2006 15:04:05 GMT", rest[:j])
	if err != nil {
		t.Fatalf("parse expiry-date %q: %v", rest[:j], err)
	}
	return expiry
}

func TestStorageClassPutAndList(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	client := ts.s3Client()
	cases := []struct {
		key   string
		class s3types.StorageClass
	}{
		{"std.txt", s3types.StorageClassStandard},
		{"ia.txt", s3types.StorageClassStandardIa},
		{"glacier.txt", s3types.StorageClassGlacier},
	}

	for _, tc := range cases {
		_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket:       aws.String(defaultBucket),
			Key:          aws.String(tc.key),
			Body:         strings.NewReader("hello"),
			StorageClass: tc.class,
		})
		ts.OK(err)
	}

	out, err := client.ListObjects(context.TODO(), &s3.ListObjectsInput{
		Bucket: aws.String(defaultBucket),
	})
	ts.OK(err)
	got := map[string]s3types.ObjectStorageClass{}
	for _, obj := range out.Contents {
		got[*obj.Key] = obj.StorageClass
	}
	for _, tc := range cases {
		if got[tc.key] != s3types.ObjectStorageClass(tc.class) {
			t.Fatalf("%s: got storage class %q, want %q", tc.key, got[tc.key], tc.class)
		}
	}

	// STANDARD_IA is immediately readable.
	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("ia.txt"),
	})
	ts.OK(err)

	head, err := client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("glacier.txt"),
	})
	ts.OK(err)
	if head.StorageClass != s3types.StorageClassGlacier {
		t.Fatalf("head storage class: got %q, want GLACIER", head.StorageClass)
	}
}

func TestStorageClassInvalid(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, err := http.NewRequest("PUT", ts.url("/"+defaultBucket+"/bad.txt"), strings.NewReader("x"))
	ts.OK(err)
	req.Header.Set("Content-Length", "1")
	req.Header.Set("x-amz-storage-class", "NOT_A_CLASS")
	resp, err := http.DefaultClient.Do(req)
	ts.OK(err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestDeepArchiveRequiresRestore(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(0)),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("deep.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassDeepArchive,
	})
	ts.OK(err)

	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("deep.txt"),
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState, got %v", err)
	}

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("deep.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	ts.OK(err)

	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("deep.txt"),
	})
	ts.OK(err)
}

func TestGlacierIRImmediatelyReadable(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("ir.txt"),
		Body:         strings.NewReader("instant"),
		StorageClass: s3types.StorageClassGlacierIr,
	})
	ts.OK(err)

	out, err := client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("ir.txt"),
	})
	ts.OK(err)
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(out.Body)
	out.Body.Close()
	if body.String() != "instant" {
		t.Fatalf("body: got %q", body.String())
	}

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("ir.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState for GLACIER_IR restore, got %v", err)
	}
}

func TestGlacierGetRequiresRestore(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(time.Hour)),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState, got %v", err)
	}

	// Head still succeeds and has no restore header yet.
	head, err := client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	assertRestoreNone(t, head.Restore)
}

func TestRestoreObjectAsyncAndExpiry(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(time.Hour)),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	ts.OK(err)

	head, err := client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	assertRestoreOngoing(t, head.Restore)

	// Still unrestored: Get fails; second Restore is in-progress.
	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState while restoring, got %v", err)
	}
	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	if !hasErrorCode(err, gofakes3.ErrRestoreAlreadyInProgress) {
		t.Fatalf("expected RestoreAlreadyInProgress, got %v", err)
	}

	// Advance past restore delay: object becomes readable.
	ts.Advance(time.Hour)
	getOut, err := client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(getOut.Body)
	getOut.Body.Close()
	if body.String() != "frozen" {
		t.Fatalf("body: got %q", body.String())
	}

	head, err = client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	assertRestoreAvailable(t, head.Restore)

	// Advance past 1-day expiry: archived again; restore header cleared.
	ts.Advance(24 * time.Hour)
	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState after expiry, got %v", err)
	}
	head, err = client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	assertRestoreNone(t, head.Restore)

	// After expiry, a new Restore starts async restore again.
	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	ts.OK(err)
	head, err = client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	assertRestoreOngoing(t, head.Restore)
}

func TestRestoreObjectRenewal(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(0)),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	ts.OK(err)

	// Delay is 0 so it is immediately available.
	head1, err := client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	assertRestoreAvailable(t, head1.Restore)
	expiry1 := restoreExpiryTime(t, *head1.Restore)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(2),
		},
	})
	ts.OK(err)

	head2, err := client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	assertRestoreAvailable(t, head2.Restore)
	expiry2 := restoreExpiryTime(t, *head2.Restore)
	if !expiry2.After(expiry1) {
		t.Fatalf("expected renewed expiry after %v, got %v", expiry1, expiry2)
	}

	// After 1 day, still readable due to renewal (+2 days from original expiry).
	ts.Advance(24 * time.Hour)
	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)

	// After another 2 days past the extended expiry, archived again.
	ts.Advance(2 * 24 * time.Hour)
	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState after renewed expiry, got %v", err)
	}
}

func TestRestoreObjectDaysBounds(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(0)),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	for _, days := range []int32{0, 366} {
		_, err := client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
			Bucket: aws.String(defaultBucket),
			Key:    aws.String("cold.txt"),
			RestoreRequest: &s3types.RestoreRequest{
				Days: aws.Int32(days),
			},
		})
		if !hasErrorCode(err, gofakes3.ErrInvalidArgument) {
			t.Fatalf("days=%d: expected InvalidArgument, got %v", days, err)
		}
	}

	for _, days := range []int32{1, 365} {
		_, err := client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
			Bucket: aws.String(defaultBucket),
			Key:    aws.String("cold.txt"),
			RestoreRequest: &s3types.RestoreRequest{
				Days: aws.Int32(days),
			},
		})
		ts.OK(err)
		// After first successful restore (delay 0), subsequent call is a renewal.
		if days == 1 {
			// leave available for the Days=365 renewal
			continue
		}
	}
}

func TestRestoreObjectNonGlacier(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("warm.txt"),
		Body:         strings.NewReader("warm"),
		StorageClass: s3types.StorageClassStandard,
	})
	ts.OK(err)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("warm.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState, got %v", err)
	}
}

func TestCopyObjectUnrestoredGlacierFails(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(0)),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	_, err = client.CopyObject(context.TODO(), &s3.CopyObjectInput{
		Bucket:     aws.String(defaultBucket),
		Key:        aws.String("copy.txt"),
		CopySource: aws.String(defaultBucket + "/cold.txt"),
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState, got %v", err)
	}

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	ts.OK(err)

	// Copy without StorageClass defaults destination to STANDARD (AWS behaviour).
	_, err = client.CopyObject(context.TODO(), &s3.CopyObjectInput{
		Bucket:     aws.String(defaultBucket),
		Key:        aws.String("copy.txt"),
		CopySource: aws.String(defaultBucket + "/cold.txt"),
	})
	ts.OK(err)

	head, err := client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("copy.txt"),
	})
	ts.OK(err)
	if head.StorageClass != s3types.StorageClassStandard {
		t.Fatalf("copy storage class: got %q, want STANDARD", head.StorageClass)
	}
	assertRestoreNone(t, head.Restore)

	out, err := client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("copy.txt"),
	})
	ts.OK(err)
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(out.Body)
	out.Body.Close()
	if body.String() != "frozen" {
		t.Fatalf("copy body: got %q", body.String())
	}

	// Explicit GLACIER destination stays archived and needs its own restore.
	_, err = client.CopyObject(context.TODO(), &s3.CopyObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("copy-glacier.txt"),
		CopySource:   aws.String(defaultBucket + "/cold.txt"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	head, err = client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("copy-glacier.txt"),
	})
	ts.OK(err)
	if head.StorageClass != s3types.StorageClassGlacier {
		t.Fatalf("glacier copy storage class: got %q, want GLACIER", head.StorageClass)
	}
	assertRestoreNone(t, head.Restore)

	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("copy-glacier.txt"),
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState for unrestored glacier copy, got %v", err)
	}
}

func TestDeepArchiveRejectsExpedited(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(0)),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("deep.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassDeepArchive,
	})
	ts.OK(err)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("deep.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
			GlacierJobParameters: &s3types.GlacierJobParameters{
				Tier: s3types.TierExpedited,
			},
		},
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidArgument) {
		t.Fatalf("expected InvalidArgument for DEEP_ARCHIVE Expedited, got %v", err)
	}

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("deep.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
			GlacierJobParameters: &s3types.GlacierJobParameters{
				Tier: s3types.TierStandard,
			},
		},
	})
	ts.OK(err)

	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("deep.txt"),
	})
	ts.OK(err)

	// After expiry, Bulk must also succeed.
	ts.Advance(24 * time.Hour)
	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("deep.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
			GlacierJobParameters: &s3types.GlacierJobParameters{
				Tier: s3types.TierBulk,
			},
		},
	})
	ts.OK(err)
}

func TestRestoreObjectRenewalFromNow(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(
			gofakes3.WithRestoreDelay(0),
			gofakes3.WithRestoreRenewal(gofakes3.RestoreRenewalFromNow),
		),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(3),
		},
	})
	ts.OK(err)

	head1, err := client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	assertRestoreAvailable(t, head1.Restore)
	expiry1 := restoreExpiryTime(t, *head1.Restore)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	ts.OK(err)

	head2, err := client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
	assertRestoreAvailable(t, head2.Restore)
	expiry2 := restoreExpiryTime(t, *head2.Restore)
	if !expiry2.Before(expiry1) {
		t.Fatalf("FromNow renewal should shorten expiry from %v to earlier, got %v", expiry1, expiry2)
	}

	// After 1 day from renewal, archived again.
	ts.Advance(24 * time.Hour)
	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState after FromNow expiry, got %v", err)
	}
}

func TestRestoreObjectMaxDaysOption(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(
			gofakes3.WithRestoreDelay(0),
			gofakes3.WithRestoreMaxDays(3),
		),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(4),
		},
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidArgument) {
		t.Fatalf("days=4: expected InvalidArgument, got %v", err)
	}

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(3),
		},
	})
	ts.OK(err)
}

func TestRestoreObjectTierExpedited(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(time.Hour)),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
			GlacierJobParameters: &s3types.GlacierJobParameters{
				Tier: s3types.TierExpedited,
			},
		},
	})
	ts.OK(err)

	// Expedited uses delay 0 even when Standard delay is 1h.
	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
	})
	ts.OK(err)
}

func TestRestoreObjectTierInvalid(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(0)),
	)
	defer ts.Close()

	client := ts.s3Client()
	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	req, err := http.NewRequest("POST", ts.url("/"+defaultBucket+"/cold.txt?restore"), strings.NewReader(
		`<RestoreRequest xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Days>1</Days><GlacierJobParameters><Tier>Nope</Tier></GlacierJobParameters></RestoreRequest>`,
	))
	ts.OK(err)
	req.Header.Set("Content-Type", "application/xml")
	resp, err := http.DefaultClient.Do(req)
	ts.OK(err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestRestoreObjectDoesNotCreateVersion(t *testing.T) {
	ts := newTestServer(t, withVersioning(),
		withFakerOptions(gofakes3.WithRestoreDelay(0)),
	)
	defer ts.Close()

	client := ts.s3Client()
	putOut, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)
	if putOut.VersionId == nil || *putOut.VersionId == "" {
		t.Fatal("expected version id from put")
	}

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	ts.OK(err)

	versions, err := client.ListObjectVersions(context.TODO(), &s3.ListObjectVersionsInput{
		Bucket: aws.String(defaultBucket),
		Prefix: aws.String("cold.txt"),
	})
	ts.OK(err)
	if len(versions.Versions) != 1 {
		t.Fatalf("expected 1 version after restore, got %d", len(versions.Versions))
	}
	if *versions.Versions[0].VersionId != *putOut.VersionId {
		t.Fatalf("version id changed: put %q list %q", *putOut.VersionId, *versions.Versions[0].VersionId)
	}
}

func TestRestoreObjectWithVersionId(t *testing.T) {
	ts := newTestServer(t, withVersioning(),
		withFakerOptions(gofakes3.WithRestoreDelay(0)),
	)
	defer ts.Close()

	client := ts.s3Client()
	putOut, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("cold.txt"),
		Body:         strings.NewReader("frozen"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket:    aws.String(defaultBucket),
		Key:       aws.String("cold.txt"),
		VersionId: putOut.VersionId,
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	ts.OK(err)

	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket:    aws.String(defaultBucket),
		Key:       aws.String("cold.txt"),
		VersionId: putOut.VersionId,
	})
	ts.OK(err)
}

func TestMultipartGlacierRequiresRestore(t *testing.T) {
	ts := newTestServer(t,
		withFakerOptions(gofakes3.WithRestoreDelay(0)),
	)
	defer ts.Close()

	client := ts.s3Client()
	mpu, err := client.CreateMultipartUpload(context.TODO(), &s3.CreateMultipartUploadInput{
		Bucket:       aws.String(defaultBucket),
		Key:          aws.String("mpu-cold.txt"),
		StorageClass: s3types.StorageClassGlacier,
	})
	ts.OK(err)

	part, err := client.UploadPart(context.TODO(), &s3.UploadPartInput{
		Bucket:     aws.String(defaultBucket),
		Key:        aws.String("mpu-cold.txt"),
		UploadId:   mpu.UploadId,
		PartNumber: aws.Int32(1),
		Body:       strings.NewReader("part-one"),
	})
	ts.OK(err)

	_, err = client.CompleteMultipartUpload(context.TODO(), &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(defaultBucket),
		Key:      aws.String("mpu-cold.txt"),
		UploadId: mpu.UploadId,
		MultipartUpload: &s3types.CompletedMultipartUpload{
			Parts: []s3types.CompletedPart{{
				ETag:       part.ETag,
				PartNumber: aws.Int32(1),
			}},
		},
	})
	ts.OK(err)

	_, err = client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("mpu-cold.txt"),
	})
	if !hasErrorCode(err, gofakes3.ErrInvalidObjectState) {
		t.Fatalf("expected InvalidObjectState, got %v", err)
	}

	_, err = client.RestoreObject(context.TODO(), &s3.RestoreObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("mpu-cold.txt"),
		RestoreRequest: &s3types.RestoreRequest{
			Days: aws.Int32(1),
		},
	})
	ts.OK(err)

	out, err := client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(defaultBucket),
		Key:    aws.String("mpu-cold.txt"),
	})
	ts.OK(err)
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(out.Body)
	out.Body.Close()
	if body.String() != "part-one" {
		t.Fatalf("body: got %q", body.String())
	}
}
