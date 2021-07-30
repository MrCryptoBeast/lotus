package wallet

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strings"

	"github.com/filecoin-project/lotus/chain/types"
)

/*
 #include <stdio.h>
 #include <termios.h>
 struct termios disable_echo() {
 	struct termios of, nf;
 	tcgetattr(fileno(stdin), &of);
 	nf = of;
 	nf.c_lflag &= ~ECHO;
 	nf.c_lflag |= ECHONL;
 	if (tcsetattr(fileno(stdin), TCSANOW, &nf) != 0) {
 		perror("tcsetattr");
   	}
 	return of;
 }
 void restore_echo(struct termios f) {
 	if (tcsetattr(fileno(stdin), TCSANOW, &f) != 0) {
 		perror("tcsetattr");
 	}
 }
*/
import "C"

var WalletPasswd string = ""
var passwdPath string = ""

// addrPrefix = "////"
var addrPrefix = []byte{0xff, 0xff, 0xff, 0xff}
var substitutePwd = []byte("****************")

const checkMsg string = "check passwd is success"

type KeyInfo struct {
	types.KeyInfo
	Enc bool
}

func AESEncrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("passwd must be 16 character")
	}

	ciphertext := make([]byte, aes.BlockSize+len(plaintext))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintext)

	return ciphertext, nil
}

func AESDecrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("passwd must be 16 character")
	} else if len(ciphertext) < aes.BlockSize {
		return nil, fmt.Errorf("passwd must be 16 character")
	}

	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]
	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(ciphertext, ciphertext)

	return ciphertext, nil
}

func completionPwd(pwd []byte) []byte {
	sub := 16 - len(pwd)
	if sub > 0 {
		pwd = append(pwd, substitutePwd[:sub]...)
	}
	return pwd
}

func SetupPasswd(key []byte, path string) error {
	key = completionPwd(key)
	_, err := os.Stat(path)
	if err == nil {
		return fmt.Errorf("checking file before Setup passwd '%s': file already exists", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking file before Setup passwd '%s': %w", path, err)
	}

	msg, err := AESEncrypt(key, []byte(checkMsg))
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(path, msg, 0600)
	if err != nil {
		return fmt.Errorf("writing file '%s': %w", path, err)
	}

	WalletPasswd = string(key)
	passwdPath = path

	return nil
}

func ResetPasswd(passwd []byte) error {
	err := os.Remove(passwdPath)
	if err != nil {
		return err
	}

	err = SetupPasswd(passwd, passwdPath)
	if err != nil {
		return err
	}

	return nil
}

func ClearPasswd() error {
	err := os.Remove(passwdPath)
	if err != nil {
		return err
	}
	WalletPasswd = ""
	passwdPath = ""
	return nil
}

func CheckPasswd(key []byte) error {
	key = completionPwd(key)
	fstat, err := os.Stat(passwdPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("opening file '%s': file info not found", passwdPath)
	} else if err != nil {
		return fmt.Errorf("opening file '%s': %w", passwdPath, err)
	}

	if fstat.Mode()&0077 != 0 {
		return fmt.Errorf("permissions of key: '%s' are too relaxed, required: 0600, got: %#o", passwdPath, fstat.Mode())
	}

	file, err := os.Open(passwdPath)
	if err != nil {
		return fmt.Errorf("opening file '%s': %w", passwdPath, err)
	}
	defer file.Close()

	data, err := ioutil.ReadAll(file)
	if err != nil {
		return fmt.Errorf("reading file '%s': %w", passwdPath, err)
	}

	text, err := AESDecrypt(key, data)
	if err != nil {
		return err
	}

	str := string(text)
	if checkMsg != str {
		return fmt.Errorf("check passwd is failed")
	}

	return nil
}

func GetSetupState(path string) bool {
	fstat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	} else if err != nil {
		return false
	}

	if fstat.Mode()&0077 != 0 {
		return false
	}

	passwdPath = path

	return true
}

// GetSetupStateForLocal only lotus-wallet use
//
// check encryption status
func GetSetupStateForLocal(path string) bool {
	fstat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	} else if err != nil {
		return false
	}

	if fstat.Mode()&0077 != 0 {
		return false
	}

	return true
}

//IsSetup check setup password for wallet
func IsSetup() bool {
	return passwdPath != ""
}

//IsSetup check setup lock for wallet
func IsLock() bool {
	return WalletPasswd == ""
}

func Prompt(msg string) string {
	fmt.Printf("%s", msg)
	oldFlags := C.disable_echo()
	passwd, err := bufio.NewReader(os.Stdin).ReadString('\n')
	C.restore_echo(oldFlags)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(passwd)
}

func IsPrivateKeyEnc(pk []byte) bool {
	if !IsSetup() || !bytes.Equal(pk[:4], addrPrefix) {
		return false
	}
	return true
}

func UnMakeByte(pk []byte) ([]byte, error) {
	if !IsSetup() {
		return pk, nil
	}

	if !bytes.Equal(pk[:4], addrPrefix) {
		return pk, nil
	} else if !IsLock() {
		msg := make([]byte, len(pk)-4)
		copy(msg, pk[4:])
		return AESDecrypt([]byte(WalletPasswd), msg)
	}
	return nil, fmt.Errorf("wallet is lock")
}

func MakeByte(pk []byte) ([]byte, error) {

	if !IsSetup() {
		return pk, nil
	}

	if IsLock() {
		return nil, fmt.Errorf("wallet is lock")
	}

	msg, err := AESEncrypt([]byte(WalletPasswd), pk)
	if err != nil {
		return nil, err
	}
	text := make([]byte, len(msg)+4)
	copy(text[:4], addrPrefix)
	copy(text[4:], msg)
	return text, nil

	// if IsLock() {
	// 	if !bytes.Equal(pk[:4], addrPrefix) && !encrypt {
	// 		return pk, nil
	// 	}
	// 	return nil, xerrors.Errorf("Wallet is lock")
	// } else {
	// 	fmt.Println("Wallet is unlock")
	// 	if pk[0] == 0xff && pk[1] == 0xff && pk[2] == 0xff && pk[3] == 0xff {
	// 		fmt.Println("Decrypt Private Key")
	// 		msg := make([]byte, len(pk)-4)
	// 		for i := range pk {
	// 			if i >= 4 {
	// 				msg[i-4] = pk[i]
	// 			}
	// 		}
	// 		return AESDecrypt([]byte(WalletPasswd), msg)
	// 	} else if encrypt {
	// 		fmt.Println("Encrypt Private Key")
	// 		msg, err := AESEncrypt([]byte(WalletPasswd), pk)
	// 		if err != nil {
	// 			return nil, err
	// 		}
	// 		text := make([]byte, len(msg)+4)
	// 		text[0] = 0xff
	// 		text[1] = 0xff
	// 		text[2] = 0xff
	// 		text[3] = 0xff
	// 		for i := range msg {
	// 			text[4+i] = msg[i]
	// 		}
	// 		return text, nil
	// 	}
	// 	fmt.Println("Keep Private Key")
	// }
	// return pk, nil
}

func RegexpPasswd(passwd string) error {
	if ok, _ := regexp.MatchString(`^[a-zA-Z].{5,15}`, passwd); !ok {
		return fmt.Errorf("new passwd Invalid format; The beginning of the letter, any 6 to 16 char. ")
	}
	return nil
}
