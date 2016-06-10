package gcredstash

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/kms"
	"strconv"
	"strings"
)

func getMaterial(name string, version string, table string) (map[string]*dynamodb.AttributeValue, error) {
	svc := dynamodb.New(session.New())

	var material map[string]*dynamodb.AttributeValue

	if version == "" {
		params := &dynamodb.QueryInput{
			TableName:                aws.String(table),
			Limit:                    aws.Int64(1),
			ConsistentRead:           aws.Bool(true),
			ScanIndexForward:         aws.Bool(false),
			KeyConditionExpression:   aws.String("#name = :name"),
			ExpressionAttributeNames: map[string]*string{"#name": aws.String("name")},
			ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
				":name": {S: aws.String(name)},
			},
		}

		resp, err := svc.Query(params)

		if err != nil {
			return nil, err
		}

		if *resp.Count == 0 {
			return nil, fmt.Errorf("Item {'name': '%s'} couldn't be found.", name)
		}

		material = resp.Items[0]
	} else {
		params := &dynamodb.GetItemInput{
			TableName: aws.String(table),
			Key: map[string]*dynamodb.AttributeValue{
				"name":    {S: aws.String(name)},
				"version": {S: aws.String(version)},
			},
		}

		resp, err := svc.GetItem(params)

		if err != nil {
			return nil, err
		}

		if resp.Item == nil {
			return nil, fmt.Errorf("Item {'name': '%s'} couldn't be found.", name)
		}

		material = resp.Item
	}

	return material, nil
}

func doHmac(message []byte, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

func checkMAC(message []byte, hmacStr *string, key []byte) bool {
	expectedMAC := doHmac(message, key)
	messageMAC, err := hex.DecodeString(*hmacStr)

	if err != nil {
		panic(err)
	}

	return hmac.Equal(messageMAC, expectedMAC)
}

func cryptAES(contents []byte, key []byte) []byte {
	block, err := aes.NewCipher(key)

	if err != nil {
		panic(err)
	}

	iv := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}
	text := make([]byte, len(contents))

	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(text, contents)

	return text
}

func decryptMaterial(name string, material map[string]*dynamodb.AttributeValue, context map[string]string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(*material["key"].S)

	if err != nil {
		panic(err)
	}

	svc := kms.New(session.New())

	params := &kms.DecryptInput{
		CiphertextBlob: data,
	}

	if len(context) > 0 {
		encCtx := map[string]*string{}

		for key, value := range context {
			encCtx[key] = aws.String(value)
		}

		params.EncryptionContext = encCtx
	}

	resp, err := svc.Decrypt(params)

	if err != nil {
		if strings.Contains(err.Error(), "InvalidCiphertextException") {
			if len(context) < 1 {
				return "", fmt.Errorf("%s: Could not decrypt hmac key with KMS. The credential may require that an encryption context be provided to decrypt it.", name)
			} else {
				return "", fmt.Errorf("%s: Could not decrypt hmac key with KMS. The encryption context provided may not match the one used when the credential was stored.", name)
			}
		} else {
			return "", err
		}
	}

	key := resp.Plaintext[:32]
	hmacKey := resp.Plaintext[32:]

	contents, err := base64.StdEncoding.DecodeString(*material["contents"].S)

	if err != nil {
		return "", err
	}

	if !checkMAC(contents, material["hmac"].S, hmacKey) {
		return "", fmt.Errorf("Computed HMAC on %s does not match stored HMAC", name)
	}

	plainText := cryptAES(contents, key)

	return string(plainText), nil
}

func GetHighestVersion(name string, table string) (int, error) {
	svc := dynamodb.New(session.New())

	params := &dynamodb.QueryInput{
		TableName:                aws.String(table),
		Limit:                    aws.Int64(1),
		ConsistentRead:           aws.Bool(true),
		ScanIndexForward:         aws.Bool(false),
		KeyConditionExpression:   aws.String("#name = :name"),
		ExpressionAttributeNames: map[string]*string{"#name": aws.String("name")},
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":name": {S: aws.String(name)},
		},
		ProjectionExpression: aws.String("version"),
	}

	resp, err := svc.Query(params)

	if err != nil {
		return -1, err
	}

	if *resp.Count == 0 {
		return 0, nil

	}

	version := *resp.Items[0]["version"].S
	ver, err := strconv.Atoi(version)

	if err != nil {
		panic(err)
	}

	return ver, nil
}

func generateDataKey(kmsKey string, context map[string]string) (*kms.GenerateDataKeyOutput, error) {
	svc := kms.New(session.New())

	params := &kms.GenerateDataKeyInput{
		KeyId:         aws.String(kmsKey),
		NumberOfBytes: aws.Int64(64),
	}

	if len(context) > 0 {
		encCtx := map[string]*string{}

		for key, value := range context {
			encCtx[key] = aws.String(value)
		}

		params.EncryptionContext = encCtx
	}

	resp, err := svc.GenerateDataKey(params)

	if err != nil {
		return nil, fmt.Errorf("Could not generate key using KMS key %s", kmsKey)
	}

	return resp, nil
}

func putItem(name string, version string, key []byte, contents []byte, hmac []byte, table string) error {
	b64key := base64.StdEncoding.EncodeToString(key)
	b64contents := base64.StdEncoding.EncodeToString(contents)
	hexHmac := hex.EncodeToString(hmac)

	svc := dynamodb.New(session.New())

	params := &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item: map[string]*dynamodb.AttributeValue{
			"name":     {S: aws.String(name)},
			"version":  {S: aws.String(version)},
			"key":      {S: aws.String(b64key)},
			"contents": {S: aws.String(b64contents)},
			"hmac":     {S: aws.String(hexHmac)},
		},
		ConditionExpression:      aws.String("attribute_not_exists(#name)"),
		ExpressionAttributeNames: map[string]*string{"#name": aws.String("name")},
	}

	_, err := svc.PutItem(params)

	if err != nil {
		return err
	}

	return nil
}

func getDeleteSecrets(name string, version string, table string) (map[*string]*string, error) {
	svc := dynamodb.New(session.New())
	items := map[*string]*string{}

	if version == "" {
		params := &dynamodb.ScanInput{
			TableName:                aws.String(table),
			ProjectionExpression:     aws.String("#name,version"),
			FilterExpression:         aws.String("#name = :name"),
			ExpressionAttributeNames: map[string]*string{"#name": aws.String("name")},
			ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
				":name": {S: aws.String(name)},
			},
		}

		resp, err := svc.Scan(params)

		if err != nil {
			return nil, err
		}

		if *resp.Count == 0 {
			return nil, fmt.Errorf("Item {'name': '%s'} couldn't be found.", name)
		}

		for _, i := range resp.Items {
			items[i["name"].S] = i["version"].S
		}
	} else {
		params := &dynamodb.GetItemInput{
			TableName: aws.String(table),
			Key: map[string]*dynamodb.AttributeValue{
				"name":    {S: aws.String(name)},
				"version": {S: aws.String(version)},
			},
		}

		resp, err := svc.GetItem(params)

		if err != nil {
			return nil, err
		}

		if resp.Item == nil {
			ver, err := strconv.Atoi(version)

			if err != nil {
				panic(err)
			}

			return nil, fmt.Errorf("Item {'name': '%s', 'version': %d} couldn't be found.", name, ver)
		}

		items[resp.Item["name"].S] = resp.Item["version"].S
	}

	return items, nil
}

func deleteItem(name *string, version *string, table string) error {
	svc := dynamodb.New(session.New())

	params := &dynamodb.DeleteItemInput{
		TableName: aws.String(table),
		Key: map[string]*dynamodb.AttributeValue{
			"name":    {S: name},
			"version": {S: version},
		},
	}

	_, err := svc.DeleteItem(params)

	if err != nil {
		return err
	}

	return nil
}

func DeleteSecrets(name string, version string, table string) error {
	items, err := getDeleteSecrets(name, version, table)

	if err != nil {
		return err
	}

	for name, version := range items {
		err := deleteItem(name, version, table)

		if err != nil {
			return err
		}

		ver, err := strconv.Atoi(*version)

		if err != nil {
			panic(err)
		}

		fmt.Printf("Deleting %s -- version %d\n", *name, ver)
	}

	return nil
}

func PutSecret(name string, secret string, version string, kmsKey string, table string, context map[string]string) error {
	kmsResp, err := generateDataKey(kmsKey, context)

	if err != nil {
		return err
	}

	dataKey := kmsResp.Plaintext[:32]
	hmacKey := kmsResp.Plaintext[32:]
	wrappedKey := kmsResp.CiphertextBlob

	cipherText := cryptAES([]byte(secret), dataKey)
	hmac := doHmac(cipherText, hmacKey)

	err = putItem(name, version, wrappedKey, cipherText, hmac, table)

	if err != nil {
		if strings.Contains(err.Error(), "ConditionalCheckFailedException") {
			latestVersion, err := GetHighestVersion(name, table)

			if err != nil {
				return err
			}

			return fmt.Errorf(
				"%s version %d is already in the credential store. Use the -v flag to specify a new version",
				name,
				latestVersion)
		} else {
			return err
		}
	}

	return nil
}

func GetSecret(name string, version string, table string, context map[string]string) (string, error) {
	material, err := getMaterial(name, version, table)

	if err != nil {
		return "", err
	}

	plainText, err := decryptMaterial(name, material, context)

	if err != nil {
		return "", err
	}

	return plainText, nil
}

func ListSecrets(table string) (map[*string]*string, error) {
	svc := dynamodb.New(session.New())

	params := &dynamodb.ScanInput{
		TableName:                aws.String(table),
		ProjectionExpression:     aws.String("#name,version"),
		ExpressionAttributeNames: map[string]*string{"#name": aws.String("name")},
	}

	resp, err := svc.Scan(params)

	if err != nil {
		return nil, err
	}

	items := map[*string]*string{}

	for _, i := range resp.Items {
		items[i["name"].S] = i["version"].S
	}

	return items, nil
}

func isTableExits(table string) (bool, error) {
	svc := dynamodb.New(session.New())
	params := &dynamodb.ListTablesInput{}
	exist := false

	err := svc.ListTablesPages(params, func(page *dynamodb.ListTablesOutput, lastPage bool) bool {
		for _, tableName := range page.TableNames {
			if *tableName == table {
				exist = true
				return false
			}
		}

		return true
	})

	if err != nil {
		return false, err
	}

	return exist, nil
}

func createTable(table string) error {
	svc := dynamodb.New(session.New())

	params := &dynamodb.CreateTableInput{
		TableName: aws.String(table),
		KeySchema: []*dynamodb.KeySchemaElement{
			{
				AttributeName: aws.String("name"),
				KeyType:       aws.String("HASH"),
			},
			{
				AttributeName: aws.String("version"),
				KeyType:       aws.String("RANGE"),
			},
		},
		AttributeDefinitions: []*dynamodb.AttributeDefinition{
			{
				AttributeName: aws.String("name"),
				AttributeType: aws.String("S"),
			},
			{
				AttributeName: aws.String("version"),
				AttributeType: aws.String("S"),
			},
		},
		ProvisionedThroughput: &dynamodb.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(1),
			WriteCapacityUnits: aws.Int64(1),
		},
	}

	_, err := svc.CreateTable(params)

	return err
}

func waitUntilTableExists(table string) error {
	svc := dynamodb.New(session.New())

	params := &dynamodb.DescribeTableInput{
		TableName: aws.String(table),
	}

	return svc.WaitUntilTableExists(params)
}

func CreateDdbTable(table string) error {
	exist, err := isTableExits(table)

	if err != nil {
		return err
	}

	if exist {
		return fmt.Errorf("Credential Store table already exists -- %s", table)
	}

	err = createTable(table)

	if err != nil {
		return err
	}

	fmt.Println("Creating table...")
	fmt.Println("Waiting for table to be created...")

	err = waitUntilTableExists(table)

	if err != nil {
		return err
	}

	fmt.Println("Table has been created. Go read the README about how to create your KMS key")

	return nil
}
