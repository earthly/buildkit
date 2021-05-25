
FROM alpine:3.13
WORKDIR /buildkit

build:
    FROM DOCKERFILE --target buildkit-buildkitd-linux .

code:
    COPY . .
    SAVE ARTIFACT /buildkit

image:
    FROM +build
    SAVE IMAGE earthly/raw-buildkitd:latest

# @#
test-image:
    FROM alpine:3.13
    RUN echo "hello world 4" >/content
    SAVE IMAGE --insecure --push 172.17.0.3:1234/test/test:latest

multi:
    BUILD --platform=linux/amd64 --platform=linux/arm64 +test-image2

test-image2:
    FROM alpine:3.13
    RUN echo "hello world 12" >/content
    SAVE IMAGE test/test3:latest
