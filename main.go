package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/net/websocket"

	"github.com/go-redis/redis"
	_ "github.com/go-sql-driver/mysql"
)

type httpRep struct {
	code int
	msg  string
	data map[string]string
}

type mysqlConfig struct {
	DbType   string `json:"type"`
	Host     string `json:"host"`
	User     string `json:"user"`
	Password string `json:"password"`
	Port     int    `json:"port"`
	Dbname   string `json:"dbname"`
}

type redisConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Db       int    `json:"db"`
}

type config struct {
	Redis redisConfig
	Db    mysqlConfig
}

func init() {
	var err error
	cfstr, err := ioutil.ReadFile("./config/config.json")
	checkErr(err)
	var cf config
	err = json.Unmarshal(cfstr, &cf)
	checkErr(err)

	dsn := cf.Db.User + ":" + cf.Db.Password + "@" + cf.Db.Host + ":" + strconv.Itoa(cf.Db.Port) + "/" + cf.Db.Dbname + "?charset=utf8"

	db, err = sql.Open("mysql", dsn)
	checkErr(err)
	redisCli = redis.NewClient(&redis.Options{
		Addr:     cf.Redis.Host + ":" + strconv.Itoa(cf.Redis.Port),
		Password: cf.Redis.Password,
		DB:       cf.Redis.Db,
	})
}

var httpNotAuth = httpRep{code: 403, msg: "认证失败"}
var httpInvalid = httpRep{code: 401, msg: "参数错误"}

func jsonResp(w http.ResponseWriter, httprep httpRep) {
	if httprep.code == 200 {
		jstr, _ := json.Marshal(httprep.data)
		w.Write(jstr)
	} else {
		w.WriteHeader(httprep.code)
		w.Write([]byte(httprep.msg))
	}
}

func checkErr(err error) {
	if err != nil {
		panic(err)
	}
}

var db *sql.DB
var redisCli *redis.Client

type user struct {
	ws       *websocket.Conn
	username string
}

var allUsers []user

type message struct {
	Action  string `json:"action"`
	Token   string `json:"token"`
	To      string `json:"to"`
	Message string `json:"message"`
}

type response struct {
	Action     string `json:"action"`
	Username   string `json:"username"`
	Headimgurl string `json:"headimgurl"`
	Message    string `json:"message"`
	ToUsername string `json:"toUsername"`
}

func removeWs(ws *websocket.Conn) {
	for index, user := range allUsers {
		if user.ws == ws {
			allUsers = append(allUsers[:index], allUsers[index+1:]...)
			break
		}
	}
}

//Echo echos the response to user
func Echo(ws *websocket.Conn) {
	var err error
	for {
		var reply message
		if err = websocket.JSON.Receive(ws, &reply); err != nil {
			removeWs(ws)
			break
		}
		var msg response
		switch reply.Action {
		case "connect":
			msg.Username, msg.Headimgurl = getUserinfo(reply.Token)
			if len(msg.Username) > 0 {
				msg.Action = "connected"
			} else {
				msg.Action = "close"
			}
			message, _ := json.Marshal(msg)
			websocket.Message.Send(ws, string(message))
			allUsers = append(allUsers, user{ws: ws, username: msg.Username})
			continue
		case "message":
			msg.Message = reply.Message
			msg.ToUsername = reply.To
			msg.Username, msg.Headimgurl = getUserinfo(reply.Token)
			msg.Action = "message"
		case "close":
			fmt.Println("closed")
			removeWs(ws)
			break
		}
		message, _ := json.Marshal(msg)
		if reply.To == "Go语言讨论组" {
			for _, user := range allUsers {
				websocket.Message.Send(user.ws, string(message))
			}
		} else {
			for _, user := range allUsers {
				if user.username == reply.To || user.username == msg.Username {
					websocket.Message.Send(user.ws, string(message))
				}
			}
		}

	}
}

func getUserinfo(token string) (string, string) {
	infoJSON, err := redisCli.Get(token).Result()
	if err != nil {
		fmt.Println(err.Error())
	}
	var rep response
	err = json.Unmarshal([]byte(infoJSON), &rep)
	if err != nil {
		fmt.Println(err.Error())
	}
	return rep.Username, rep.Headimgurl
}

func getToken(w http.ResponseWriter, req *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	req.ParseMultipartForm(32 << 20)
	username := req.FormValue("username")
	if len(username) == 0 {
		jsonResp(w, httpInvalid)
		return
	}
	fmt.Println(username)
	file, handler, err := req.FormFile("headimg")
	checkErr(err)
	defer file.Close()
	filename := createToken("headimg")
	f, err := os.OpenFile("./storage/uploads/"+filename+handler.Filename, os.O_WRONLY|os.O_CREATE, 0666)
	checkErr(err)
	defer f.Close()
	_, err = io.Copy(f, file)
	checkErr(err)
	token := createToken(username)
	userInfo := make(map[string]string)
	userInfo["username"] = username
	userInfo["headimgurl"] = "/uploads/" + filename + handler.Filename
	userStr, _ := json.Marshal(userInfo)
	redisCli.Set(token, userStr, 0)
	fmt.Println(token)
	jsonToken := make(map[string]string)
	jsonToken["token"] = token
	jsonResp(w, httpRep{code: 200, data: jsonToken})
}

func createToken(username string) string {
	crutime := time.Now().Unix()
	c1 := md5.Sum([]byte(string(crutime) + username))
	return hex.EncodeToString(c1[:])
}

func route() {
	http.HandleFunc("/getToken", getToken)
	http.Handle("/chat", websocket.Handler(Echo))
	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir("storage/uploads"))))
}

func main() {
	route()
	err := http.ListenAndServe(":10080", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
