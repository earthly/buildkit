
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
    RUN echo "hello world 3" >/content
    SAVE IMAGE --insecure --push 172.17.0.2:1234/test/test:latest
