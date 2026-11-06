| garage | minio | a start multipart | PASS | err= |
| garage | minio | b signed content-length | PASS | PUT size+1 status=403 err=<nil> |
| garage | minio | c complete with client etags | PASS | wrong etag=InvalidPart ok= head size=11534336 |
| garage | minio | c2 complete with parts 1 and 3 only | INFO | complete="" head size=6291456 of 11534336: server must check parts 1..n and total size itself |
| garage | minio | e PUT after complete | PASS | status=404 |
| garage | minio | f complete twice then head | PASS | second complete="NoSuchUpload" head size=11534336 |
| garage | minio | d complete many tiny parts | PASS | parts=2000 complete= took=358ms |
| garage | minio | g copy processing to completed | PASS | bytes=1073741824 copy= took=134ms |
| garage | minio | h public endpoint signing | PASS | public host status=200, same URL on internal host status=403 |
| garage | minio | i CORS put and expose ETag | PASS | preflight status=200 allow-origin="http://localhost:3000" put status=200 expose="ETag" |
| garage | minio | j headBucket classification | PASS | ok="" missing="NoSuchBucket" bad creds="AccessDenied" down="network" |
| garage | minio | k lifecycle rules | PASS | PutBucketLifecycle= |
| garage | aws | a start multipart | PASS | err= |
| garage | aws | b signed content-length | PASS | PUT size+1 status=403 err=<nil> |
| garage | aws | c complete with client etags | PASS | wrong etag=InvalidPart ok= head size=11534336 |
| garage | aws | c2 complete with parts 1 and 3 only | INFO | complete="" head size=6291456 of 11534336: server must check parts 1..n and total size itself |
| garage | aws | e PUT after complete | PASS | status=404 |
| garage | aws | f complete twice then head | PASS | second complete="NoSuchUpload" head size=11534336 |
| garage | aws | d complete many tiny parts | PASS | parts=2000 complete= took=418ms |
| garage | aws | h public endpoint signing | PASS | public host status=200, same URL on internal host status=403 |
| garage | aws | i CORS put and expose ETag | PASS | preflight status=200 allow-origin="http://localhost:3000" put status=200 expose="ETag" |
| garage | aws | j headBucket classification | PASS | ok="" missing="NotFound" bad creds="Forbidden" down="network" |
| garage | aws | k lifecycle rules | PASS | PutBucketLifecycle= |
| versitygw | minio | a start multipart | PASS | err= |
| versitygw | minio | b signed content-length | PASS | PUT size+1 status=403 err=<nil> |
| versitygw | minio | c complete with client etags | PASS | wrong etag=InvalidPart ok= head size=11534336 |
| versitygw | minio | c2 complete with parts 1 and 3 only | INFO | complete="" head size=6291456 of 11534336: server must check parts 1..n and total size itself |
| versitygw | minio | e PUT after complete | FAIL | status=0 |
| versitygw | minio | f complete twice then head | PASS | second complete="" head size=11534336 |
| versitygw | minio | d complete many tiny parts | FAIL | parts=2000 complete=EntityTooSmall took=22ms |
| versitygw | minio | d complete many 5MiB parts | PASS | parts=200 complete= took=1.151s |
| versitygw | minio | g copy processing to completed | PASS | bytes=1073741824 copy= took=8.34s |
| versitygw | minio | h public endpoint signing | PASS | public host status=200, same URL on internal host status=403 |
| versitygw | minio | i CORS put and expose ETag | PASS | preflight status=200 allow-origin="http://localhost:3000" put status=200 expose="ETag, x-amz-storage-class" |
| versitygw | minio | j headBucket classification | PASS | ok="" missing="NoSuchBucket" bad creds="AccessDenied" down="network" |
| versitygw | minio | k lifecycle rules | FAIL | PutBucketLifecycle=NotImplemented |
| versitygw | aws | a start multipart | PASS | err= |
| versitygw | aws | b signed content-length | FAIL | PUT size+1 status=0 err=Put "http://chatim-spike-vgw:7070/spike-processing/spike/aws/main/1793984406546375059?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=spikeaccesskey01%2F20261106%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20261106T170006Z&X-Amz-Expires=900&X-Amz-SignedHeaders=content-length%3Bhost&partNumber=1&uploadId=f61dd763-243d-4ec3-81cc-759734c8630d&x-id=UploadPart&X-Amz-Signature=2b275eed9b2f3974b9826f2b7eed807c6975403e59a6af724bb169d15c341a5c": readfrom tcp 192.168.97.16:53800->192.168.97.15:7070: write tcp 192.168.97.16:53800->192.168.97.15:7070: write: connection reset by peer |
| versitygw | aws | c complete with client etags | PASS | wrong etag=InvalidPart ok= head size=11534336 |
| versitygw | aws | c2 complete with parts 1 and 3 only | INFO | complete="" head size=6291456 of 11534336: server must check parts 1..n and total size itself |
| versitygw | aws | e PUT after complete | FAIL | status=0 |
| versitygw | aws | f complete twice then head | PASS | second complete="" head size=11534336 |
| versitygw | aws | d complete many tiny parts | FAIL | parts=2000 complete=EntityTooSmall took=17ms |
| versitygw | aws | d complete many 5MiB parts | PASS | parts=200 complete= took=977ms |
| versitygw | aws | h public endpoint signing | PASS | public host status=200, same URL on internal host status=403 |
| versitygw | aws | i CORS put and expose ETag | PASS | preflight status=200 allow-origin="http://localhost:3000" put status=200 expose="ETag, x-amz-storage-class" |
| versitygw | aws | j headBucket classification | PASS | ok="" missing="NotFound" bad creds="Forbidden" down="network" |
| versitygw | aws | k lifecycle rules | FAIL | PutBucketLifecycle=NotImplemented |
