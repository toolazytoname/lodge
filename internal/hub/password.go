package hub

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = uint32(3)
	argonMemory  = uint32(64 * 1024) // KiB: 64 MiB per login attempt.
	argonThreads = uint8(1)
	argonSaltLen = 16
	argonKeyLen  = 32
)

type argonParameters struct {
	time    uint32
	memory  uint32
	threads uint8
	keyLen  uint32
}

// HashPassword returns a PHC-style Argon2id verifier. The random salt makes
// equal passwords produce different stored values.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}
	if len(password) > 1024 {
		return "", errors.New("password is too long")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword performs the same bounded Argon2id work and compares the
// result in constant time. Invalid or unsafe verifier parameters fail closed.
func VerifyPassword(encoded, password string) bool {
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil || len(password) > 1024 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, params.keyLen)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func validatePasswordHash(encoded string) error {
	_, _, _, err := parsePasswordHash(encoded)
	return err
}

func parsePasswordHash(encoded string) (argonParameters, []byte, []byte, error) {
	var params argonParameters
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return params, nil, nil, errors.New("passwordHash must use Argon2id PHC format")
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil || version != argon2.Version {
		return params, nil, nil, errors.New("unsupported Argon2 version")
	}
	var memory, timeCost uint64
	var threads uint64
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return params, nil, nil, errors.New("invalid Argon2 parameters")
	}
	for _, field := range fields {
		pair := strings.SplitN(field, "=", 2)
		if len(pair) != 2 {
			return params, nil, nil, errors.New("invalid Argon2 parameter")
		}
		value, parseErr := strconv.ParseUint(pair[1], 10, 32)
		if parseErr != nil {
			return params, nil, nil, errors.New("invalid Argon2 parameter value")
		}
		switch pair[0] {
		case "m":
			memory = value
		case "t":
			timeCost = value
		case "p":
			threads = value
		default:
			return params, nil, nil, errors.New("unknown Argon2 parameter")
		}
	}
	// Config is root-controlled, but bounds prevent a corrupted file from
	// turning one login into unbounded CPU or memory consumption.
	if memory < 32*1024 || memory > 256*1024 || timeCost < 1 || timeCost > 10 || threads < 1 || threads > 8 {
		return params, nil, nil, errors.New("Argon2 parameters outside safe bounds")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return params, nil, nil, errors.New("invalid Argon2 salt")
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return params, nil, nil, errors.New("invalid Argon2 hash")
	}
	params = argonParameters{
		time:    uint32(timeCost),
		memory:  uint32(memory),
		threads: uint8(threads),
		keyLen:  uint32(len(expected)),
	}
	return params, salt, expected, nil
}
