FROM golang:1.25-alpine

# Thư mục làm việc trong container
WORKDIR /app

# Copy go.mod & go.sum trước để cache
COPY go.mod go.sum ./
RUN go mod download

# Copy toàn bộ source code vào container
COPY . .

RUN go build -o app

#Expose port API
EXPOSE 8080

# Chạy app
CMD ["./app"]
