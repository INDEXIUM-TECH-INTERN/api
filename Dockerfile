# FROM golang:1.25-alpine

# # Thư mục làm việc trong container
# WORKDIR /app

# # Copy go.mod & go.sum trước để cache
# COPY go.mod go.sum ./
# RUN go mod download

# # Copy toàn bộ source code vào container
# COPY . .

# RUN go build -o app

# #Expose port API
# EXPOSE 8080

# # Chạy app
# CMD ["./app"]

FROM golang:1.25-alpine

WORKDIR /app

# 1) copy module files trước để cache
COPY go.mod go.sum ./
RUN go mod download

# 2) copy source có chọn lọc (KHÔNG copy toàn bộ)
COPY main.go ./
COPY config ./config
COPY database ./database
COPY utils ./utils

# 3) build
RUN go build -o app

# 4) chạy non-root (Sonar cũng thích)
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
USER appuser

EXPOSE 8080
CMD ["./app"]

