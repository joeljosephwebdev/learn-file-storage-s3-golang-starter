package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const uploadLimit = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)

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

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to parse form file", err)
		return
	}
	defer file.Close()

	headers := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(headers)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "failed to parse content type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusUnsupportedMediaType, "incorrect file type. video must be an mp4", err)
		return
	}

	// create temporary local copy of video file
	tmpVideo, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to create temp video file", err)
		return
	}
	defer tmpVideo.Close()           // Close first (LIFO)
	defer os.Remove(tmpVideo.Name()) // Then remove

	// Copy uploaded file to temporary file
	_, err = io.Copy(tmpVideo, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to write content to video file", err)
		return
	}

	// reset tempFile's file pointer to the beginning
	_, err = tmpVideo.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to reset temporary video file pointer", err)
		return
	}

	// Create a unique key for S3
	key := getAssetPath(mediaType)

	// Set up the S3 input parameters and upload the asset
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        tmpVideo,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading file to S3", err)
		return
	}

	// create the new video url to point to s3 location
	url := cfg.getObjectURL(key)
	video.VideoURL = &url
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	// save new video
	video.VideoURL = &url
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to update video", err)
		return
	}

}
