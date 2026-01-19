package main

import (
	"database/sql"
	"errors"
	"fmt"
	"gin-app/config"
	"log"
	"math"
	"strconv"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
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
	r.GET("/posts", listPosts)
	r.GET("/posts/:id", GetPostByIDHandler)

	r.POST("/posts", CreatePostHandler)
	r.PUT("/posts/:id", UpdatePostHandler)
	r.DELETE("/posts/:id", DeletePostHandler)

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

	c.JSON(200, gin.H{
		"page":        page,
		"limit":       limit,
		"total_pages": totalPages,
		"data":        posts,
	})
}

func GetPostByIDHandler(c *gin.Context) {
	// 1) parse id
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		c.JSON(400, gin.H{"error": "invalid post id"})
		return
	}

	// 2) query DB (khớp struct Post của bạn)
	const q = `
        SELECT id, title, content, created_at, updated_at, thumbnail
        FROM posts
        WHERE id = ?
        LIMIT 1
    `

	var p Post
	err = DB.QueryRow(q, id).Scan(
		&p.ID,
		&p.Title,
		&p.Content,
		&p.CreatedAt,
		&p.UpdatedAt,
		&p.Thumbnail,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(404, gin.H{"error": "post not found"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"data": p})
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

// POST /posts
func CreatePostHandler(c *gin.Context) {
	var in CreatePostInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"error": "invalid json body"})
		return
	}

	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	in.Thumbnail = strings.TrimSpace(in.Thumbnail)

	if in.Title == "" {
		c.JSON(400, gin.H{"error": "title is required"})
		return
	}
	if len(in.Title) > 50 { // title VARCHAR(50)
		c.JSON(400, gin.H{"error": "title must be <= 50 characters"})
		return
	}
	if in.Content == "" {
		c.JSON(400, gin.H{"error": "content is required"})
		return
	}
	if in.Thumbnail == "" {
		c.JSON(400, gin.H{"error": "thumbnail is required"})
		return
	}
	if len(in.Thumbnail) > 255 { // thumbnail VARCHAR(255)
		c.JSON(400, gin.H{"error": "thumbnail must be <= 255 characters"})
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

	c.JSON(201, gin.H{"data": p})
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

	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" {
			c.JSON(400, gin.H{"error": "title cannot be empty"})
			return
		}
		if len(t) > 50 {
			c.JSON(400, gin.H{"error": "title must be <= 50 characters"})
			return
		}
		set = append(set, "title = ?")
		args = append(args, t)
	}

	if in.Content != nil {
		ct := strings.TrimSpace(*in.Content)
		if ct == "" {
			c.JSON(400, gin.H{"error": "content cannot be empty"})
			return
		}
		set = append(set, "content = ?")
		args = append(args, ct)
	}

	if in.Thumbnail != nil {
		th := strings.TrimSpace(*in.Thumbnail)
		if th == "" {
			c.JSON(400, gin.H{"error": "thumbnail cannot be empty"})
			return
		}
		if len(th) > 255 {
			c.JSON(400, gin.H{"error": "thumbnail must be <= 255 characters"})
			return
		}
		set = append(set, "thumbnail = ?")
		args = append(args, th)
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
		c.JSON(404, gin.H{"error": "post not found"})
		return
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
		c.JSON(404, gin.H{"error": "post not found"})
		return
	}

	c.JSON(200, gin.H{"message": "deleted", "id": id})
}
