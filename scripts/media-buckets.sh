#!/usr/bin/env bash
set -euo pipefail

container=${S3_CONTAINER:-chatim-s3}
network=${NETWORK:-chatim_default}
endpoint=${S3_INTERNAL_ENDPOINT:-http://chatim-s3:3900}
processing=${MEDIA_S3_BUCKET_PROCESSING:-chatim-media-processing}
completed=${MEDIA_S3_BUCKET_COMPLETED:-chatim-media-completed}
origin=${MEDIA_CORS_ORIGIN:-*}
aws_image=amazon/aws-cli:2.27.0
key_name=chatim-dev

: "${MEDIA_S3_ACCESS_KEY:?set MEDIA_S3_ACCESS_KEY in .env (GK followed by 24 hex digits)}"
: "${MEDIA_S3_SECRET_KEY:?set MEDIA_S3_SECRET_KEY in .env (64 hex digits)}"

garage() { docker exec "$container" /garage "$@" 2>/dev/null; }

for _ in $(seq 1 60); do
  if garage status >/dev/null; then break; fi
  sleep 1
done
garage status >/dev/null || { echo "$container not reachable" >&2; exit 1; }

if garage status | grep -q "NO ROLE ASSIGNED"; then
  node=$(garage node id -q | cut -d@ -f1)
  garage layout assign -z dev -c 20G "$node" >/dev/null
  version=$(garage layout show | grep -o 'apply --version [0-9]*' | awk '{print $3}')
  garage layout apply --version "$version" >/dev/null
  echo "layout applied (version $version)"
fi

if ! garage key info "$MEDIA_S3_ACCESS_KEY" >/dev/null; then
  garage key import --yes -n "$key_name" "$MEDIA_S3_ACCESS_KEY" "$MEDIA_S3_SECRET_KEY" >/dev/null
  echo "key $key_name imported"
fi
garage key allow --create-bucket "$MEDIA_S3_ACCESS_KEY" >/dev/null

for bucket in "$processing" "$completed"; do
  if ! garage bucket info "$bucket" >/dev/null; then
    garage bucket create "$bucket" >/dev/null
    echo "bucket $bucket created"
  fi
  garage bucket allow --read --write --owner "$bucket" --key "$MEDIA_S3_ACCESS_KEY" >/dev/null
done

aws() {
  docker run --rm --network "$network" \
    -e AWS_ACCESS_KEY_ID="$MEDIA_S3_ACCESS_KEY" -e AWS_SECRET_ACCESS_KEY="$MEDIA_S3_SECRET_KEY" -e AWS_DEFAULT_REGION=us-east-1 \
    "$aws_image" --endpoint-url "$endpoint" "$@"
}

cors=$(printf '{"CORSRules":[{"AllowedOrigins":["%s"],"AllowedMethods":["PUT","GET"],"AllowedHeaders":["*"],"ExposeHeaders":["ETag"],"MaxAgeSeconds":3600}]}' "$origin")
for bucket in "$processing" "$completed"; do
  aws s3api put-bucket-cors --bucket "$bucket" --cors-configuration "$cors"
done
echo "cors set on $processing, $completed"

lifecycle='{"Rules":[{"ID":"processing","Status":"Enabled","Filter":{"Prefix":""},"AbortIncompleteMultipartUpload":{"DaysAfterInitiation":1},"Expiration":{"Days":30}}]}'
aws s3api put-bucket-lifecycle-configuration --bucket "$processing" --lifecycle-configuration "$lifecycle"
echo "lifecycle set on $processing"
unfinished='{"Rules":[{"ID":"completed","Status":"Enabled","Filter":{"Prefix":""},"AbortIncompleteMultipartUpload":{"DaysAfterInitiation":1}}]}'
aws s3api put-bucket-lifecycle-configuration --bucket "$completed" --lifecycle-configuration "$unfinished"
echo "lifecycle set on $completed"
