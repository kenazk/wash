package aws

import (
	"context"
	"time"

	"github.com/puppetlabs/wash/journal"
	"github.com/puppetlabs/wash/plugin"

	awsSDK "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	s3Client "github.com/aws/aws-sdk-go/service/s3"
)

// listObjects is a helper that lists the objects which start with a specific
// prefix. We don't make it a method of s3Bucket because the helper's also
// used by s3ObjectPrefix. While we could pass-around the same s3Bucket object to all
// its s3ObjectPrefix children, doing so is not a good idea because (1) it is a bit overkill
// to pass-around an entire object just to access only one of its methods and (2),
// it makes it difficult to refresh the shared s3Bucket object when the original object
// is evicted from the cache.
func listObjects(ctx context.Context, client *s3Client.S3, bucket string, prefix string) ([]plugin.Entry, error) {
	request := &s3Client.ListObjectsInput{
		Bucket:    awsSDK.String(bucket),
		Prefix:    awsSDK.String(prefix),
		Delimiter: awsSDK.String("/"),
	}

	resp, err := client.ListObjectsWithContext(ctx, request)
	if err != nil {
		return nil, err
	}

	// CommonPrefixes represent S3 object prefixes; Contents represent S3 objects.
	numPrefixes := len(resp.CommonPrefixes)
	numObjects := len(resp.Contents)
	entries := make([]plugin.Entry, numPrefixes+numObjects)

	journal.Record(
		ctx,
		"(Bucket %v, Prefix %v): Retrieved %v prefixes and %v objects",
		bucket,
		prefix,
		numPrefixes,
		numObjects,
	)

	for i, p := range resp.CommonPrefixes {
		prefix := awsSDK.StringValue(p.Prefix)
		// Skip the top-level '/' prefix, it would be redundant to list it.
		if prefix == "/" {
			continue
		}
		entries[i] = newS3ObjectPrefix(bucket, prefix, client)
	}

	for i, o := range resp.Contents {
		entries[numPrefixes+i] = newS3Object(
			bucket,
			awsSDK.StringValue(o.Key),
			client,
		)
	}

	return entries, nil
}

// s3Bucket represents an S3 bucket.
type s3Bucket struct {
	plugin.EntryBase
	ctime  time.Time
	region string
	client *s3Client.S3
}

func newS3Bucket(name string, ctime time.Time, region string, client *s3Client.S3) *s3Bucket {
	bucket := &s3Bucket{
		EntryBase: plugin.NewEntry(name),
		ctime:     ctime,
		region:    region,
		client:    client,
	}

	return bucket
}

func (b *s3Bucket) Attr(ctx context.Context) (plugin.Attributes, error) {
	return plugin.Attributes{
		Ctime: b.ctime,
		Mtime: b.ctime,
		Atime: b.ctime,
	}, nil
}

func (b *s3Bucket) List(ctx context.Context) ([]plugin.Entry, error) {
	return listObjects(ctx, b.client, b.Name(), "")
}

func (b *s3Bucket) Metadata(ctx context.Context) (plugin.MetadataMap, error) {
	request := &s3Client.GetBucketTaggingInput{
		Bucket: awsSDK.String(b.Name()),
	}

	resp, err := b.client.GetBucketTaggingWithContext(ctx, request)

	var metadata plugin.MetadataMap
	if err == nil {
		metadata = plugin.ToMetadata(resp)
	} else if awserr, ok := err.(awserr.Error); ok {
		// Check if this is a NoSuchTagSet error. If yes, then that means
		// this bucket doesn't have any tags.
		//
		// NOTE: See https://github.com/boto/boto3/issues/341#issuecomment-186007537
		// if you're interested in knowing why AWS does not return
		// an empty TagSet instead of a NoSuchTagSet error
		if awserr.Code() == "NoSuchTagSet" {
			metadata = plugin.MetadataMap{}
		} else {
			return nil, err
		}
	} else {
		// We have a non-AWS related error
		return nil, err
	}

	metadata["region"] = b.region

	return metadata, nil
}
