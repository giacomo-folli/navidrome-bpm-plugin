FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -buildvcs=false -o /out/navidrome-bpm-plugin ./cmd/navidrome-bpm-plugin

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg aubio-tools \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/navidrome-bpm-plugin /usr/local/bin/navidrome-bpm-plugin
VOLUME ["/music", "/config"]
ENTRYPOINT ["navidrome-bpm-plugin"]
