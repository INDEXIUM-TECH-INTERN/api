package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"gin-app/config"
	"log"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
)

const (
	routePosts    = "/posts"
	routePostByID = "/posts/:id"

	errPostNotFound = "post not found"
)

type Post struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Thumbnail string `json:"thumbnail"`
}

var DB *sql.DB

var RDB *redis.Client
var ctx = context.Background()

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("❌ Cannot load config:", err)
	}

	DB, err = sql.Open(cfg.Database.Driver, cfg.Database.DSN())
	if err != nil {
		log.Fatal("❌ Unable to connect database:", err)
	}

	defer DB.Close()

	if err = DB.Ping(); err != nil {
		log.Fatal("unable to connect to database", err)
	}

	log.Println("✅ Connected to MySQL")

	redisAddr := os.Getenv("REDIS_ADDR")
	if strings.TrimSpace(redisAddr) == "" {
		// Không dùng cache
		RDB = nil
		log.Println("⚠️ Redis disabled (REDIS_ADDR is empty) -> running WITHOUT cache")
	} else {
		RDB = redis.NewClient(&redis.Options{
			Addr: redisAddr,
		})

		if err := RDB.Ping(ctx).Err(); err != nil {
			// Không kill app nữa, chỉ tắt cache
			log.Println("⚠️ Cannot connect Redis -> running WITHOUT cache. Err:", err)
			RDB = nil
		} else {
			log.Println("✅ Connected to Redis")
		}
	}

	// GIN SERVER
	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Authorization",
			"ngrok-skip-browser-warning",
		},
		AllowCredentials: false,
	}))

	// ===== ROUTES KHÔNG PREFIX =====
	r.GET(routePosts, listPosts)
	r.GET("/posts/search", searchAlbums)
	r.GET(routePostByID, GetPostByIDHandler)

	r.POST(routePosts, CreatePostHandler)
	r.PUT(routePostByID, UpdatePostHandler)
	r.DELETE(routePostByID, DeletePostHandler)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	log.Println("🚀 Server running at http://<IP_của_máy>:8080")
	addr := cfg.Server.Host + ":" + fmt.Sprint(cfg.Server.Port)
	r.Run(addr)

}

// GET METHOD
func listPosts(c *gin.Context) {

	// 1. Lấy query params
	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "10")
	// text_search

	// ✅ PAGE: clamp về [1..100], kể cả nhập cực lớn
	const maxPage = 100
	page := 1

	iPage, err := strconv.ParseInt(pageStr, 10, 64)
	if err != nil {
		// ❗ Phân biệt lỗi
		if errors.Is(err, strconv.ErrRange) {
			// số quá lớn (overflow)
			page = maxPage
		} else {
			// abc, ký tự lạ
			page = 1
		}
	} else if iPage <= 0 {
		// số âm hoặc 0
		page = 1
	} else if iPage > int64(maxPage) {
		// số lớn nhưng chưa overflow
		page = maxPage
	} else {
		page = int(iPage)
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 || limit > 20 {
		limit = 20
	}

	var total int
	err = DB.QueryRow("SELECT COUNT(*) FROM posts").Scan(&total)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	offset := (page - 1) * limit

	cacheKey := fmt.Sprintf("posts:list:p=%d:l=%d", page, limit)

	type ListPostsResponse struct {
		Page       int    `json:"page"`
		Limit      int    `json:"limit"`
		TotalPages int    `json:"total_pages"`
		Data       []Post `json:"data"`
	}

	if RDB != nil {
		if bs, err := RDB.Get(ctx, cacheKey).Bytes(); err == nil {
			var resp ListPostsResponse
			if jsonErr := json.Unmarshal(bs, &resp); jsonErr == nil {
				c.JSON(200, resp)
				return
			}
		}
	}

	// 2. Query có LIMIT & OFFSET
	query := `
		SELECT id, title, content, created_at, updated_at, thumbnail 
		FROM posts
		
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`
	rows, err := DB.Query(query, limit, offset)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	posts := []Post{}

	for rows.Next() {
		var p Post
		err := rows.Scan(
			&p.ID,
			&p.Title,
			&p.Content,
			&p.CreatedAt,
			&p.UpdatedAt,
			&p.Thumbnail,
		)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		posts = append(posts, p)
	}

	totalPages := int(math.Ceil(float64(total) / float64(limit)))

	resp := ListPostsResponse{
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
		Data:       posts,
	}

	// Save cache (TTL 30s)
	if RDB != nil {
		if bs, err := json.Marshal(resp); err == nil {

			_ = RDB.Set(ctx, cacheKey, bs, 30*time.Second).Err()
		}
	}

	c.JSON(200, resp)

}

func GetPostByIDHandler(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		c.JSON(400, gin.H{"error": "invalid post id"})
		return
	}

	cacheKey := fmt.Sprintf("posts:id:%d", id)

	// 1) Try cache
	if RDB != nil {
		if bs, err := RDB.Get(ctx, cacheKey).Bytes(); err == nil {
			var p Post
			if jsonErr := json.Unmarshal(bs, &p); jsonErr == nil {
				c.JSON(200, gin.H{"data": p, "cache": "hit"})
				return
			}
		}
	}

	// 2) Query DB
	const q = `
    SELECT id, title, content, created_at, updated_at, thumbnail
    FROM posts
    WHERE id = ?
    LIMIT 1
  `

	var p Post
	err = DB.QueryRow(q, id).Scan(
		&p.ID, &p.Title, &p.Content, &p.CreatedAt, &p.UpdatedAt, &p.Thumbnail,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(404, gin.H{"error": errPostNotFound})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// 3) Save cache (TTL 10 phút)
	if RDB != nil {
		if bs, err := json.Marshal(p); err == nil {
			_ = RDB.Set(ctx, cacheKey, bs, 10*time.Minute).Err()
		}
	}

	c.JSON(200, gin.H{"data": p, "cache": "miss"})
}

type CreatePostInput struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	Thumbnail string `json:"thumbnail"`
}

type UpdatePostInput struct {
	Title     *string `json:"title"`
	Content   *string `json:"content"`
	Thumbnail *string `json:"thumbnail"`
}

func parsePostID(c *gin.Context) (int64, bool) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		c.JSON(400, gin.H{"error": "invalid post id"})
		return 0, false
	}
	return id, true
}

func validateCreatePost(in *CreatePostInput) string {
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	in.Thumbnail = strings.TrimSpace(in.Thumbnail)

	if in.Title == "" {
		return "title is required"
	}
	if len(in.Title) > 50 {
		return "title must be <= 50 characters"
	}
	if in.Content == "" {
		return "content is required"
	}
	if in.Thumbnail == "" {
		return "thumbnail is required"
	}
	if len(in.Thumbnail) > 255 {
		return "thumbnail must be <= 255 characters"
	}

	return ""
}

// POST /posts
func CreatePostHandler(c *gin.Context) {
	var in CreatePostInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"error": "invalid json body"})
		return
	}

	if msg := validateCreatePost(&in); msg != "" {
		c.JSON(400, gin.H{"error": msg})
		return
	}

	res, err := DB.Exec(
		`INSERT INTO posts (title, content, thumbnail) VALUES (?, ?, ?)`,
		in.Title, in.Content, in.Thumbnail,
	)

	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	newID, err := res.LastInsertId()
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot get inserted id"})
		return
	}

	// Trả về record vừa tạo
	var p Post
	err = DB.QueryRow(`
        SELECT id, title, content, created_at, updated_at, thumbnail
        FROM posts
        WHERE id = ?
        LIMIT 1
    `, newID).Scan(
		&p.ID, &p.Title, &p.Content, &p.CreatedAt, &p.UpdatedAt, &p.Thumbnail,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	deleteByPattern("posts:list:*")
	deleteByPattern("posts:search:*")

	c.JSON(201, gin.H{"data": p})
}

func addOptionalStringField(set *[]string, args *[]any, col string, v *string, maxLen int, emptyMsg string, tooLongMsg string) string {
	if v == nil {
		return ""
	}

	s := strings.TrimSpace(*v)
	if s == "" {
		return emptyMsg
	}

	if maxLen > 0 && len(s) > maxLen {
		return tooLongMsg
	}

	*set = append(*set, col+" = ?")
	*args = append(*args, s)
	return ""
}

// PUT /posts/:id (update field nào gửi lên)
func UpdatePostHandler(c *gin.Context) {
	id, ok := parsePostID(c)
	if !ok {
		return
	}

	var in UpdatePostInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"error": "invalid json body"})
		return
	}

	set := make([]string, 0, 3)
	args := make([]any, 0, 4)

	if msg := addOptionalStringField(&set, &args, "title", in.Title, 50, "title cannot be empty", "title must be <= 50 characters"); msg != "" {
		c.JSON(400, gin.H{"error": msg})
		return
	}

	if msg := addOptionalStringField(&set, &args, "content", in.Content, 0, "content cannot be empty", ""); msg != "" {
		c.JSON(400, gin.H{"error": msg})
		return
	}

	if msg := addOptionalStringField(&set, &args, "thumbnail", in.Thumbnail, 255, "thumbnail cannot be empty", "thumbnail must be <= 255 characters"); msg != "" {
		c.JSON(400, gin.H{"error": msg})
		return
	}

	if len(set) == 0 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	q := fmt.Sprintf("UPDATE posts SET %s WHERE id = ?", strings.Join(set, ", "))
	args = append(args, id)

	res, err := DB.Exec(q, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": errPostNotFound})
		return
	}

	if RDB != nil {
		_ = RDB.Del(ctx, fmt.Sprintf("posts:id:%d", id)).Err()
		deleteByPattern("posts:list:*")
		deleteByPattern("posts:search:*")
	}

	// trả về record sau khi update
	var p Post
	err = DB.QueryRow(`
        SELECT id, title, content, created_at, updated_at, thumbnail
        FROM posts
        WHERE id = ?
        LIMIT 1
    `, id).Scan(
		&p.ID, &p.Title, &p.Content, &p.CreatedAt, &p.UpdatedAt, &p.Thumbnail,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"data": p})
}

// DELETE /posts/:id
func DeletePostHandler(c *gin.Context) {
	id, ok := parsePostID(c)
	if !ok {
		return
	}

	res, err := DB.Exec(`DELETE FROM posts WHERE id = ?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": errPostNotFound})
		return
	}

	if RDB != nil {
		_ = RDB.Del(ctx, fmt.Sprintf("posts:id:%d", id)).Err()
		deleteByPattern("posts:list:*")
		deleteByPattern("posts:search:*")
	}

	c.JSON(200, gin.H{"message": "deleted", "id": id})
}

func deleteByPattern(pattern string) {
	if RDB == nil {
		return
	}

	var cursor uint64
	for {
		keys, next, err := RDB.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return
		}

		if len(keys) > 0 {
			_ = RDB.Del(ctx, keys...).Err()
		}

		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func buildSearchCacheKey(title, content string) string {
	title = strings.TrimSpace(strings.ToLower(title))
	content = strings.TrimSpace(strings.ToLower(content))

	return fmt.Sprintf(
		"posts:search:title=%s:content=%s",
		url.QueryEscape(title),
		url.QueryEscape(content),
	)
}

func searchAlbums(c *gin.Context) {
	title := strings.TrimSpace(c.Query("title"))
	content := strings.TrimSpace(c.Query("content"))

	bypassCache := c.Query("nocache") != ""

	cacheKey := buildSearchCacheKey(title, content)

	// 1) Try cache trước
	if !bypassCache && RDB != nil {
		if bs, err := RDB.Get(c.Request.Context(), cacheKey).Bytes(); err == nil {
			c.Header("X-Cache", "HIT")
			c.Data(200, "application/json; charset=utf-8", bs)
			return
		}
	}

	// 2) Build query
	query := `
		SELECT id, title, content, created_at, updated_at, thumbnail
		FROM posts
	`
	var conditions []string
	var args []any

	if title != "" {
		conditions = append(conditions, "title LIKE ?")
		args = append(args, "%"+title+"%")
	}

	if content != "" {
		conditions = append(conditions, "content LIKE ?")
		args = append(args, "%"+content+"%")
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " OR ")
	}

	query += " ORDER BY created_at DESC"

	rows, err := DB.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	result := make([]Post, 0)
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.Title, &p.Content, &p.CreatedAt, &p.UpdatedAt, &p.Thumbnail); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		result = append(result, p)
	}

	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// 3) Tạo response JSON
	resp := gin.H{"data": result}

	bs, err := json.Marshal(resp)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// 4) Save cache
	if !bypassCache && RDB != nil {
		_ = RDB.Set(c.Request.Context(), cacheKey, bs, 5*time.Minute).Err()
	}

	if bypassCache {
		c.Header("X-Cache", "BYPASS")
	} else {
		c.Header("X-Cache", "MISS")
	}
	c.Data(200, "application/json; charset=utf-8", bs)
}
