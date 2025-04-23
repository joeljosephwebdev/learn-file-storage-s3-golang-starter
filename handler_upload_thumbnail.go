package main

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory int64 = 10 << 20

	r.ParseMultipartForm(maxMemory)
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to parse form file", err)
		return
	}

	headers := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(headers)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "failed to parse content type", err)
		return
	}

	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusUnsupportedMediaType, "incorrect file type. thumbnail must be an image", err)
		return
	}

	imageData, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to read data", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "couldn't get video", err)
		return
	}

	if video.UserID != userID {
		err := fmt.Errorf("user %d is not the owner of video %d", userID, videoID)
		respondWithError(w, http.StatusUnauthorized, "you are not authorized to access this video", err)
		return
	}

	fileIDString := CreateFileID()
	filePath, err := saveToFile(imageData, mediaType, cfg.assetsRoot, fileIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to save thumbnail", err)
		return
	}

	thumbnailUrl := fmt.Sprintf("http://localhost:8091/%s", filePath)
	video.ThumbnailURL = &thumbnailUrl
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func saveToFile(data []byte, mediaType, assetsRoot, fileIDString string) (string, error) {
	extension, err := extractFileExtension(mediaType)
	if err != nil {
		return "", fmt.Errorf("%s is not a valid content-type\n%v", mediaType, err)
	}
	fileName := fmt.Sprintf("%s.%s", fileIDString, extension)
	filePath := filepath.Join(assetsRoot, fileName)

	file, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create new file %s\n%v", filePath, err)
	}

	_, err = io.Copy(file, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to copy image to file %s\n%v", filePath, err)
	}

	return filePath, nil
}

func extractFileExtension(contentType string) (string, error) {
	parts := strings.Split(contentType, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid content type: %s", contentType)
	}
	return parts[1], nil
}
