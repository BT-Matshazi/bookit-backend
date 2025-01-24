package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "path/filepath"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/google/uuid"
    "github.com/gorilla/mux"
)

type UploadResponse struct {
    URLs map[string]string `json:"urls"`
}

type S3Client struct {
    client *s3.Client
    bucket string
}

func NewS3Client() (*S3Client, error) {
    cfg, err := config.LoadDefaultConfig(context.TODO(),
        config.WithRegion(os.Getenv("AWS_REGION")),
    )
    if err != nil {
        return nil, fmt.Errorf("unable to load SDK config: %v", err)
    }

    client := s3.NewFromConfig(cfg)
    return &S3Client{
        client: client,
        bucket: os.Getenv("AWS_BUCKET"),
    }, nil
}

func (s *S3Client) uploadToS3(ctx context.Context, file []byte, directory string, filename string) (string, error) {
    // Generate unique filename
    ext := filepath.Ext(filename)
    uniqueFilename := fmt.Sprintf("%s/%s%s", directory, uuid.New().String(), ext)
    
    // Create the input for PutObject
    input := &s3.PutObjectInput{
        Bucket:      &s.bucket,
        Key:         &uniqueFilename,
        Body:        bytes.NewReader(file),
        ContentType: aws.String(http.DetectContentType(file)),
    }

    // Upload the file
    _, err := s.client.PutObject(ctx, input)
    if err != nil {
        return "", fmt.Errorf("failed to upload file: %v", err)
    }

    // Return the URL
    return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", 
        s.bucket, 
        os.Getenv("AWS_REGION"), 
        uniqueFilename), nil
}

func handleUpload(s3Client *S3Client) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Set CORS headers
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }

        // Set max upload size - 10 MB
        r.ParseMultipartForm(10 << 20)

        // Get directory from form
        directory := r.FormValue("directory")
        if directory == "" {
            directory = "uploads" // default directory
        }

        // Get tags from form
        tags := r.MultipartForm.File
        if len(tags) == 0 {
            http.Error(w, "No files uploaded", http.StatusBadRequest)
            return
        }

        urls := make(map[string]string)
        ctx := context.Background()

        // Iterate through each tag and its files
        for tag, files := range tags {
            if len(files) == 0 {
                continue
            }

            // Use the first file for each tag
            fileHeader := files[0]
            
            // Open the file
            file, err := fileHeader.Open()
            if err != nil {
                http.Error(w, fmt.Sprintf("Error opening file: %v", err), http.StatusInternalServerError)
                return
            }
            defer file.Close()

            // Read file content
            fileBytes, err := io.ReadAll(file)
            if err != nil {
                http.Error(w, fmt.Sprintf("Error reading file: %v", err), http.StatusInternalServerError)
                return
            }

            // Upload to S3 with directory
            url, err := s3Client.uploadToS3(ctx, fileBytes, directory, fileHeader.Filename)
            if err != nil {
                http.Error(w, fmt.Sprintf("Error uploading to S3: %v", err), http.StatusInternalServerError)
                return
            }

            urls[tag] = url
        }

        // Return response
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(UploadResponse{URLs: urls})
    }
}

func main() {
    // Initialize S3 client
    s3Client, err := NewS3Client()
    if err != nil {
        log.Fatalf("Failed to create S3 client: %v", err)
    }

    // Set up router
    r := mux.NewRouter()
    r.HandleFunc("/upload", handleUpload(s3Client)).Methods("POST", "OPTIONS")

    // Start server
    port := os.Getenv("PORT")
    if port == "" {
        port = "8000"
    }
    
    log.Printf("Server starting on port %s", port)
    if err := http.ListenAndServe(":"+port, r); err != nil {
        log.Fatal(err)
    }
}