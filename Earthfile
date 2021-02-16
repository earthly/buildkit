
FROM alpine:3.11
WORKDIR /buildkit

build:
    FROM DOCKERFILE --target buildkit-buildkitd-linux .
    ARG EARTHLY_TARGET_TAG_DOCKER
    ARG TAG=$EARTHLY_TARGET_TAG_DOCKER
    SAVE IMAGE --push earthly/buildkit:$TAG

debug: 
    FROM DOCKERFILE --build-arg EXTRA_BUILD_OPTS="-gcflags='all=-N -l'" --target buildkit-buildkitd-linux .
    ARG EARTHLY_TARGET_TAG_DOCKER
    ARG TAG=$EARTHLY_TARGET_TAG_DOCKER
    SAVE IMAGE --push earthly/buildkit:debug-$TAG

code:
    COPY . .
    SAVE ARTIFACT /buildkit
