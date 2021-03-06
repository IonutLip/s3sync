# Copyright 2019 SEQSENSE, Inc.
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
.PHONY: test
test:
	go test .

.PHONY: cover
cover:
	go test -coverprofile=cover.out .
	go tool cover -html=cover.out -o report.html

.PHONY: s3
s3:
	docker run -p 4572:4572 -e SERVICES=s3 localstack/localstack

.PHONY: fixture
fixture:
	aws s3 --endpoint-url http://localhost:4572 mb s3://example-bucket
	aws s3 --endpoint-url http://localhost:4572 cp README.md s3://example-bucket
	aws s3 --endpoint-url http://localhost:4572 cp README.md s3://example-bucket/foo/
	aws s3 --endpoint-url http://localhost:4572 cp README.md s3://example-bucket/bar/baz/

