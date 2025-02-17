package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/BattlesnakeOfficial/rules"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"
)

type Battlesnake struct {
	URL       string
	Name      string
	ID        string
	API       string
	LastMove  string
	Squad     string
	Character rune
}

type Coord struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
}

type SnakeResponse struct {
	Id      string  `json:"id"`
	Name    string  `json:"name"`
	Health  int32   `json:"health"`
	Body    []Coord `json:"body"`
	Latency int32   `json:"latency"`
	Head    Coord   `json:"head"`
	Length  int32   `json:"length"`
	Shout   string  `json:"shout"`
	Squad   string  `json:"squad"`
}

type BoardResponse struct {
	Height  int32           `json:"height"`
	Width   int32           `json:"width"`
	Food    []Coord         `json:"food"`
	Hazards []Coord         `json:"hazards"`
	Snakes  []SnakeResponse `json:"snakes"`
}

type GameResponse struct {
	Id      string `json:"id"`
	Timeout int32  `json:"timeout"`
}

type ResponsePayload struct {
	Game  GameResponse  `json:"game"`
	Turn  int32         `json:"turn"`
	Board BoardResponse `json:"board"`
	You   SnakeResponse `json:"you"`
}

type PlayerResponse struct {
	Move  string `json:"move"`
	Shout string `json:"shout"`
}

type PingResponse struct {
	APIVersion string `json:"apiversion"`
	Author     string `json:"author"`
	Color      string `json:"color"`
	Head       string `json:"head"`
	Tail       string `json:"tail"`
	Version    string `json:"version"`
}

var GameId string
var Turn int32
var Battlesnakes map[string]Battlesnake
var HttpClient http.Client
var Width int32
var Height int32
var Names []string
var URLs []string
var Squads []string
var Timeout int32
var Sequential bool
var GameType string
var ViewMap bool
var Seed int64

var playCmd = &cobra.Command{
	Use:   "play",
	Short: "Play a game of Battlesnake locally.",
	Long:  "Play a game of Battlesnake locally.",
	Run:   run,
}

func init() {
	rootCmd.AddCommand(playCmd)

	playCmd.Flags().Int32VarP(&Width, "width", "W", 11, "Width of Board")
	playCmd.Flags().Int32VarP(&Height, "height", "H", 11, "Height of Board")
	playCmd.Flags().StringArrayVarP(&Names, "name", "n", nil, "Name of Snake")
	playCmd.Flags().StringArrayVarP(&URLs, "url", "u", nil, "URL of Snake")
	playCmd.Flags().StringArrayVarP(&Names, "squad", "S", nil, "Squad of Snake")
	playCmd.Flags().Int32VarP(&Timeout, "timeout", "t", 500, "Request Timeout")
	playCmd.Flags().BoolVarP(&Sequential, "sequential", "s", false, "Use Sequential Processing")
	playCmd.Flags().StringVarP(&GameType, "gametype", "g", "standard", "Type of Game Rules")
	playCmd.Flags().BoolVarP(&ViewMap, "viewmap", "v", false, "View the Map Each Turn")
	playCmd.Flags().Int64VarP(&Seed, "seed", "r", time.Now().UTC().UnixNano(), "Random Seed")
}

var run = func(cmd *cobra.Command, args []string) {
	rand.Seed(Seed)

	Battlesnakes = make(map[string]Battlesnake)
	GameId = uuid.New().String()
	Turn = 0

	snakes := buildSnakesFromOptions()

	var ruleset rules.Ruleset
	var royale rules.RoyaleRuleset
	var outOfBounds []rules.Point
	ruleset, _ = getRuleset(Seed, Turn, snakes)
	state := initializeBoardFromArgs(ruleset, snakes)
	for _, snake := range snakes {
		Battlesnakes[snake.ID] = snake
	}

	for v := false; !v; v, _ = ruleset.IsGameOver(state) {
		Turn++
		ruleset, royale = getRuleset(Seed, Turn, snakes)
		state, outOfBounds = createNextBoardState(ruleset, royale, state, outOfBounds, snakes)
		gameOver, _ := ruleset.IsGameOver(state)
		if ViewMap && !gameOver {
			printMap(state, outOfBounds, Turn)
		} else {
			log.Printf("[%v]: State: %v OutOfBounds: %v\n", Turn, state, outOfBounds)
		}
	}

	if GameType == "solo" {
		log.Printf("[DONE]: Game completed after %v turns.", Turn)
		for _, snake := range snakes {
			sendEndRequest(state, snake)
		}
	} else {
		var winner string
		isDraw := true
		for _, snake := range state.Snakes {
			if snake.EliminatedCause == rules.NotEliminated {
				isDraw = false
				winner = Battlesnakes[snake.ID].Name
			}
			log.Printf("[DONE]: Sending end request to %v.", snake.ID)
			sendEndRequest(state, Battlesnakes[snake.ID])
		}

		if isDraw {
			log.Printf("[DONE]: Game completed after %v turns. It was a draw.", Turn)
		} else {
			log.Printf("[DONE]: Game completed after %v turns. %v is the winner.", Turn, winner)
		}
	}
}

func getRuleset(seed int64, gameTurn int32, snakes []Battlesnake) (rules.Ruleset, rules.RoyaleRuleset) {
	var ruleset rules.Ruleset
	var royale rules.RoyaleRuleset

	standard := rules.StandardRuleset{
		FoodSpawnChance: 15,
		MinimumFood:     1,
	}

	switch GameType {
	case "royale":
		royale = rules.RoyaleRuleset{
			StandardRuleset:   standard,
			Seed:              seed,
			Turn:              gameTurn,
			ShrinkEveryNTurns: 10,
			DamagePerTurn:     1,
		}
		ruleset = &royale
	case "squad":
		squadMap := map[string]string{}
		for _, snake := range snakes {
			squadMap[snake.ID] = snake.Squad
		}
		ruleset = &rules.SquadRuleset{
			StandardRuleset:     standard,
			SquadMap:            squadMap,
			AllowBodyCollisions: true,
			SharedElimination:   true,
			SharedHealth:        true,
			SharedLength:        true,
		}
	case "solo":
		ruleset = &rules.SoloRuleset{
			StandardRuleset: standard,
		}
	case "constrictor":
		ruleset = &rules.ConstrictorRuleset{
			StandardRuleset: standard,
		}
	default:
		ruleset = &standard
	}
	return ruleset, royale
}

func initializeBoardFromArgs(ruleset rules.Ruleset, snakes []Battlesnake) *rules.BoardState {
	if Timeout == 0 {
		Timeout = 500
	}
	HttpClient = http.Client{
		Timeout: time.Duration(Timeout) * time.Millisecond,
	}

	snakeIds := []string{}
	for _, snake := range snakes {
		snakeIds = append(snakeIds, snake.ID)
	}
	state, err := ruleset.CreateInitialBoardState(Width, Height, snakeIds)
	if err != nil {
		log.Panic("[PANIC]: Error Initializing Board State")
		panic(err)
	}
	for _, snake := range snakes {
		requestBody := getIndividualBoardStateForSnake(state, snake, nil)
		u, _ := url.ParseRequestURI(snake.URL)
		u.Path = path.Join(u.Path, "start")
		_, err = HttpClient.Post(u.String(), "application/json", bytes.NewBuffer(requestBody))
		if err != nil {
			log.Printf("[WARN]: Request to %v failed", u.String())
		}
	}
	return state
}

func createNextBoardState(ruleset rules.Ruleset, royale rules.RoyaleRuleset, state *rules.BoardState, outOfBounds []rules.Point, snakes []Battlesnake) (*rules.BoardState, []rules.Point) {
	var moves []rules.SnakeMove
	if Sequential {
		for _, snake := range snakes {
			moves = append(moves, getMoveForSnake(state, snake, outOfBounds))
		}
	} else {
		c := make(chan rules.SnakeMove, len(snakes))
		for _, snake := range snakes {
			go getConcurrentMoveForSnake(state, snake, outOfBounds, c)
		}
		for range snakes {
			moves = append(moves, <-c)
		}
	}
	for _, move := range moves {
		snake := Battlesnakes[move.ID]
		snake.LastMove = move.Move
		Battlesnakes[move.ID] = snake
	}
	if GameType == "royale" {
		_, err := royale.CreateNextBoardState(state, moves)
		if err != nil {
			log.Panic("[PANIC]: Error Producing Next Royale Board State")
			panic(err)
		}
	}
	state, err := ruleset.CreateNextBoardState(state, moves)
	if err != nil {
		log.Panic("[PANIC]: Error Producing Next Board State")
		panic(err)
	}
	return state, royale.OutOfBounds
}

func getConcurrentMoveForSnake(state *rules.BoardState, snake Battlesnake, outOfBounds []rules.Point, c chan rules.SnakeMove) {
	c <- getMoveForSnake(state, snake, outOfBounds)
}

func getMoveForSnake(state *rules.BoardState, snake Battlesnake, outOfBounds []rules.Point) rules.SnakeMove {
	requestBody := getIndividualBoardStateForSnake(state, snake, outOfBounds)
	u, _ := url.ParseRequestURI(snake.URL)
	u.Path = path.Join(u.Path, "move")
	res, err := HttpClient.Post(u.String(), "application/json", bytes.NewBuffer(requestBody))
	move := snake.LastMove
	if err != nil {
		log.Printf("[WARN]: Request to %v failed\n", u.String())
		log.Printf("Body --> %v\n", string(requestBody))
	} else if res.Body != nil {
		defer res.Body.Close()
		body, readErr := ioutil.ReadAll(res.Body)
		if readErr != nil {
			log.Fatal(readErr)
		} else {
			playerResponse := PlayerResponse{}
			jsonErr := json.Unmarshal(body, &playerResponse)
			if jsonErr != nil {
				log.Fatal(jsonErr)
			} else {
				move = playerResponse.Move
			}
		}
	}
	return rules.SnakeMove{ID: snake.ID, Move: move}
}

func sendEndRequest(state *rules.BoardState, snake Battlesnake) {
	requestBody := getIndividualBoardStateForSnake(state, snake, nil)
	u, _ := url.ParseRequestURI(snake.URL)
	u.Path = path.Join(u.Path, "end")
	_, err := HttpClient.Post(u.String(), "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		log.Printf("[WARN]: Request to %v failed", u.String())
	}
}

func getIndividualBoardStateForSnake(state *rules.BoardState, snake Battlesnake, outOfBounds []rules.Point) []byte {
	var youSnake rules.Snake
	for _, snk := range state.Snakes {
		if snake.ID == snk.ID {
			youSnake = snk
			break
		}
	}
	response := ResponsePayload{
		Game: GameResponse{Id: GameId, Timeout: Timeout},
		Turn: Turn,
		Board: BoardResponse{
			Height:  state.Height,
			Width:   state.Width,
			Food:    coordFromPointArray(state.Food),
			Hazards: coordFromPointArray(outOfBounds),
			Snakes:  buildSnakesResponse(state.Snakes),
		},
		You: snakeResponseFromSnake(youSnake),
	}
	responseJson, err := json.Marshal(response)
	if err != nil {
		log.Panic("[PANIC]: Error Marshalling JSON from State")
		panic(err)
	}
	return responseJson
}

func snakeResponseFromSnake(snake rules.Snake) SnakeResponse {
	health := snake.Health
	if snake.EliminatedCause == rules.EliminatedByOutOfHealth {
		health = 0
	}
	return SnakeResponse{
		Id:      snake.ID,
		Name:    Battlesnakes[snake.ID].Name,
		Health:  health,
		Body:    coordFromPointArray(snake.Body),
		Latency: 0,
		Head:    coordFromPoint(snake.Body[0]),
		Length:  int32(len(snake.Body)),
		Shout:   "",
		Squad:   Battlesnakes[snake.ID].Squad,
	}
}

func buildSnakesResponse(snakes []rules.Snake) []SnakeResponse {
	// var a []SnakeResponse
	a := make([]SnakeResponse, 0, len(snakes))
	for _, snake := range snakes {
		rsp := snakeResponseFromSnake(snake)
		if snake.EliminatedCause == rules.NotEliminated {
			a = append(a, rsp)
		}
	}
	return a
}

func coordFromPoint(pt rules.Point) Coord {
	return Coord{X: pt.X, Y: pt.Y}
}

func coordFromPointArray(ptArray []rules.Point) []Coord {
	a := make([]Coord, 0)
	for _, pt := range ptArray {
		a = append(a, coordFromPoint(pt))
	}
	return a
}

func buildSnakesFromOptions() []Battlesnake {
	bodyChars := []rune{'■', '⌀', '●', '⍟', '◘', '☺', '□', '☻'}
	var numSnakes int
	var snakes []Battlesnake
	numNames := len(Names)
	numURLs := len(URLs)
	numSquads := len(Squads)
	if numNames > numURLs {
		numSnakes = numNames
	} else {
		numSnakes = numURLs
	}
	if numNames != numURLs {
		log.Println("[WARN]: Number of Names and URLs do not match: defaults will be applied to missing values")
	}
	for i := int(0); i < numSnakes; i++ {
		var snakeName string
		var snakeURL string
		var snakeSquad string

		id := uuid.New().String()

		if i < numNames {
			snakeName = Names[i]
		} else {
			log.Printf("[WARN]: Name for URL %v is missing: a default name will be applied\n", URLs[i])
			snakeName = id
		}

		if i < numURLs {
			u, err := url.ParseRequestURI(URLs[i])
			if err != nil {
				log.Printf("[WARN]: URL %v is not valid: a default will be applied\n", URLs[i])
				snakeURL = "https://example.com"
			} else {
				snakeURL = u.String()
			}
		} else {
			log.Printf("[WARN]: URL for Name %v is missing: a default URL will be applied\n", Names[i])
			snakeURL = "https://example.com"
		}

		if GameType == "squad" {
			if i < numSquads {
				snakeSquad = Squads[i]
			} else {
				log.Printf("[WARN]: Squad for URL %v is missing: a default squad will be applied\n", URLs[i])
				snakeSquad = strconv.Itoa(i / 2)
			}
		}
		res, err := HttpClient.Get(snakeURL)
		api := "0"
		if err != nil {
			log.Printf("[WARN]: Request to %v failed", snakeURL)
		} else if res.Body != nil {
			defer res.Body.Close()
			body, readErr := ioutil.ReadAll(res.Body)
			if readErr != nil {
				log.Fatal(readErr)
			}

			pingResponse := PingResponse{}
			jsonErr := json.Unmarshal(body, &pingResponse)
			if jsonErr != nil {
				log.Fatal(jsonErr)
			} else {
				api = pingResponse.APIVersion
			}
		}
		snake := Battlesnake{Name: snakeName, URL: snakeURL, ID: id, API: api, LastMove: "up", Character: bodyChars[i%8]}
		if GameType == "squad" {
			snake.Squad = snakeSquad
		}
		snakes = append(snakes, snake)
	}
	return snakes
}

func printMap(state *rules.BoardState, outOfBounds []rules.Point, gameTurn int32) {
	var o bytes.Buffer
	o.WriteString(fmt.Sprintf("Ruleset: %s, Seed: %d, Turn: %v\n", GameType, Seed, gameTurn))
	board := make([][]rune, state.Width)
	for i := range board {
		board[i] = make([]rune, state.Height)
	}
	for y := int32(0); y < state.Height; y++ {
		for x := int32(0); x < state.Width; x++ {
			board[x][y] = '◦'
		}
	}
	for _, oob := range outOfBounds {
		board[oob.X][oob.Y] = '░'
	}
	o.WriteString(fmt.Sprintf("Hazards ░: %v\n", outOfBounds))
	for _, f := range state.Food {
		board[f.X][f.Y] = '⚕'
	}
	o.WriteString(fmt.Sprintf("Food ⚕: %v\n", state.Food))
	for _, s := range state.Snakes {
		for _, b := range s.Body {
			if !b.IsOutOfBounds(state.Width, state.Height) {
				board[b.X][b.Y] = Battlesnakes[s.ID].Character
			}
		}
		o.WriteString(fmt.Sprintf("%v %c: %v\n", Battlesnakes[s.ID].Name, Battlesnakes[s.ID].Character, s))
	}
	for y := state.Height - 1; y >= 0; y-- {
		for x := int32(0); x < state.Width; x++ {
			o.WriteRune(board[x][y])
		}
		o.WriteString("\n")
	}
	log.Print(o.String())
}
