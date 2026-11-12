package pairing

import "io"

const (
	answerLength      = 1
	answerNo     byte = 0
	answerYes    byte = 1
)

func writeMessage(writer io.Writer, message []byte) error {
	_, err := writer.Write(append([]byte{byte(len(message))}, message...))
	return err
}

func readMessage(reader io.Reader, limit int) ([]byte, error) {
	var length [1]byte
	if _, err := io.ReadFull(reader, length[:]); err != nil {
		return nil, err
	}
	if length[0] == 0 || int(length[0]) > limit {
		return nil, ErrMalformed
	}
	message := make([]byte, length[0])
	if _, err := io.ReadFull(reader, message); err != nil {
		return nil, err
	}
	return message, nil
}

func writeAnswer(writer io.Writer, yes bool) error {
	answer := answerNo
	if yes {
		answer = answerYes
	}
	return writeMessage(writer, []byte{answer})
}

func readAnswer(reader io.Reader) (bool, error) {
	message, err := readMessage(reader, answerLength)
	if err != nil {
		return false, err
	}
	switch message[0] {
	case answerYes:
		return true, nil
	case answerNo:
		return false, nil
	}
	return false, ErrMalformed
}

func readUntilGone(reader io.Reader) error {
	var unexpected [1]byte
	if _, err := io.ReadFull(reader, unexpected[:]); err != nil {
		return err
	}
	return ErrMalformed
}
