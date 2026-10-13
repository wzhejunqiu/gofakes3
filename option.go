package gofakes3

import "time"

type Option func(g *GoFakeS3)

// WithTimeSource allows you to substitute the behaviour of time.Now() and
// time.Since() within GoFakeS3. This can be used to trigger time skew errors,
// or to ensure the output of the commands is deterministic.
//
// See gofakes3.FixedTimeSource(), gofakes3.LocalTimeSource(tz).
func WithTimeSource(timeSource TimeSource) Option {
	return func(g *GoFakeS3) { g.timeSource = timeSource }
}

// WithTimeSkewLimit allows you to reconfigure the allowed skew between the
// client's clock and the server's clock. The AWS client SDKs will send the
// "x-amz-date" header containing the time at the client, which is used to
// calculate the skew.
//
// See DefaultSkewLimit for the starting value, set to '0' to disable.
func WithTimeSkewLimit(skew time.Duration) Option {
	return func(g *GoFakeS3) { g.timeSkew = skew }
}

// WithMetadataSizeLimit allows you to reconfigure the maximum allowed metadata
// size.
//
// See DefaultMetadataSizeLimit for the starting value, set to '0' to disable.
func WithMetadataSizeLimit(size int) Option {
	return func(g *GoFakeS3) { g.metadataSizeLimit = size }
}

// WithIntegrityCheck enables or disables Content-MD5 validation when
// putting an Object.
func WithIntegrityCheck(check bool) Option {
	return func(g *GoFakeS3) { g.integrityCheck = check }
}

// WithLogger allows you to supply a logger to GoFakeS3 for debugging/tracing.
// logger may be nil.
func WithLogger(logger Logger) Option {
	return func(g *GoFakeS3) { g.log = logger }
}

// WithGlobalLog configures gofakes3 to use GlobalLog() for logging, which uses
// the standard library's log.Println() call to log messages.
func WithGlobalLog() Option {
	return WithLogger(GlobalLog())
}

// WithRequestID sets the starting ID used to generate the "x-amz-request-id"
// header.
func WithRequestID(id uint64) Option {
	return func(g *GoFakeS3) { g.requestID = id }
}

// WithHostBucket enables or disables bucket rewriting in the router.
// If active, the URL 'http://mybucket.localhost/object' will be routed
// as if the URL path was '/mybucket/object'.
//
// This will apply to all requests. If you want to be more specific, provide
// WithHostBucketBase.
//
// See https://docs.aws.amazon.com/AmazonS3/latest/dev/UsingBucket.html for details.
func WithHostBucket(enabled bool) Option {
	return func(g *GoFakeS3) { g.hostBucket = enabled }
}

// WithHostBucketBase enables or disables bucket rewriting in the router, but only if the
// host is a subdomain of the base.
//
// If set to 'example.com', the URL 'http://mybucket.example.com/object' will be routed as
// if the URL path was '/mybucket/object', but 'http://example.com/bucket/object' will use
// path-based bucket routing instead.
//
// You may pass multiple bases, they are tested in order.
//
// See https://docs.aws.amazon.com/AmazonS3/latest/dev/UsingBucket.html for details.
func WithHostBucketBase(hosts ...string) Option {
	return func(g *GoFakeS3) { g.hostBucketBases = hosts }
}

// WithoutVersioning disables versioning on the passed backend, if it supported it.
func WithoutVersioning() Option {
	return func(g *GoFakeS3) { g.versioned = nil }
}

// WithUnimplementedPageError allows you to enable or disable the error that occurs
// if the Backend does not implement paging.
//
// By default, GoFakeS3 will simply retry a request for a page of objects
// without the page if the Backend does not implement pagination. This can
// be used to enable an error in that condition instead.
func WithUnimplementedPageError() Option {
	return func(g *GoFakeS3) { g.failOnUnimplementedPage = true }
}

// WithAutoBucket instructs GoFakeS3 to create buckets that don't exist on first use,
// rather than returning ErrNoSuchBucket.
func WithAutoBucket(enabled bool) Option {
	return func(g *GoFakeS3) { g.autoBucket = enabled }
}

// WithInsecureCORS responds with * for all Access-Control-Allow headers.
func WithInsecureCORS() Option {
	return func(g *GoFakeS3) { g.wrapCORS = wrapInsecureCORS }
}

// WithRestoreDelay configures how long a GLACIER/DEEP_ARCHIVE RestoreObject
// request stays in the restoring (ongoing-request="true") state before the
// temporary copy becomes readable (Standard tier). Defaults to
// DefaultRestoreDelay (1s). Tests typically combine this with WithTimeSource /
// TimeSourceAdvancer to advance time without waiting.
//
// GlacierJobParameters.Tier scales this delay: Expedited=0, Standard=1x, Bulk=2x.
func WithRestoreDelay(d time.Duration) Option {
	return func(g *GoFakeS3) { g.restoreDelay = d }
}

// WithRestoreMaxDays sets the maximum allowed Days value on RestoreObject.
// Defaults to RestoreDaysMax (365). Values less than 1 fall back to RestoreDaysMax.
func WithRestoreMaxDays(n int) Option {
	return func(g *GoFakeS3) {
		if n < RestoreDaysMin {
			n = RestoreDaysMax
		}
		g.restoreMaxDays = n
	}
}

// WithRestoreRenewal selects how RestoreObject extends an already-available
// temporary copy. Defaults to RestoreRenewalExtend (add Days to current expiry).
func WithRestoreRenewal(mode RestoreRenewalMode) Option {
	return func(g *GoFakeS3) { g.restoreRenewal = mode }
}
