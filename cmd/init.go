package cmd

import (
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"

	"time"

	"strings"

	"math/rand"

	"github.com/ananthakumaran/paisa/internal/model/price"
	"github.com/ananthakumaran/paisa/internal/scraper/mutualfund"
	"github.com/ananthakumaran/paisa/internal/scraper/nps"
	"github.com/ananthakumaran/paisa/internal/utils"
	"github.com/google/btree"
	"github.com/samber/lo"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const START_YEAR = 2014

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "generates a sample config and journal file",
	Run: func(cmd *cobra.Command, args []string) {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatal(err)
		}

		generateConfigFile(cwd)
		generateJournalFile(cwd)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

type GeneratorState struct {
	Balance      float64
	EPFBalance   float64
	Ledger       *os.File
	YearlySalary float64
	Rent         float64
	NiftyBalance float64
}

var pricesTree map[string]*btree.BTree

func generateConfigFile(cwd string) {
	configFilePath := filepath.Join(cwd, "paisa.yaml")
	config := `
journal_path: '%s'
db_path: '%s'
allocation_targets:
  - name: Debt
    target: 40
    accounts:
      - Assets:Debt:*
  - name: Equity
    target: 60
    accounts:
      - Assets:Equity:*
commodities:
  - name: NIFTY
    type: mutualfund
    code: 120716
    harvest: 365
    tax_category: equity
  - name: NIFTY_JR
    type: mutualfund
    code: 120684
    harvest: 365
    tax_category: equity
  - name: ABCBF
    type: mutualfund
    code: 119533
    harvest: 1095
    tax_category: debt
  - name: NPS_HDFC_E
    type: nps
    code: SM008001
  - name: NPS_HDFC_C
    type: nps
    code: SM008002
  - name: NPS_HDFC_G
    type: nps
    code: SM008003
`
	log.Info("Generating config file: ", configFilePath)
	journalFilePath := filepath.Join(cwd, "personal.ledger")
	dbFilePath := filepath.Join(cwd, "paisa.db")
	err := ioutil.WriteFile(configFilePath, []byte(fmt.Sprintf(config, journalFilePath, dbFilePath)), 0644)
	if err != nil {
		log.Fatal(err)
	}
}

func emitTransaction(file *os.File, date time.Time, payee string, from string, to string, amount float64) {
	_, err := file.WriteString(fmt.Sprintf(`
%s %s
    %s                                %s INR
    %s
`, date.Format("2006/01/02"), payee, to, formatFloat(amount), from))
	if err != nil {
		log.Fatal(err)
	}
}

func emitCommodityBuy(file *os.File, date time.Time, commodity string, from string, to string, amount float64) float64 {
	pc := utils.BTreeDescendFirstLessOrEqual(pricesTree[commodity], price.Price{Date: date})
	units := amount / pc.Value
	_, err := file.WriteString(fmt.Sprintf(`
%s Investment
    %s                      %s %s @    %s INR
    %s
`, date.Format("2006/01/02"), to, formatFloat(units), commodity, formatFloat(pc.Value), from))
	if err != nil {
		log.Fatal(err)
	}
	return units
}

func emitCommoditySell(file *os.File, date time.Time, commodity string, from string, to string, amount float64, availableUnits float64) (float64, float64) {
	pc := utils.BTreeDescendFirstLessOrEqual(pricesTree[commodity], price.Price{Date: date})
	requiredUnits := amount / pc.Value
	units := math.Min(availableUnits, requiredUnits)
	return emitCommodityBuy(file, date, commodity, from, to, -units*pc.Value), units * pc.Value
}

func loadPrices(schemeCode string, commodityType price.CommodityType, commodityName string, pricesTree map[string]*btree.BTree) {
	var prices []*price.Price
	var err error

	switch commodityType {
	case price.MutualFund:
		prices, err = mutualfund.GetNav(schemeCode, commodityName)
	case price.NPS:
		prices, err = nps.GetNav(schemeCode, commodityName)
	}

	if err != nil {
		log.Fatal(err)
	}

	pricesTree[commodityName] = btree.New(2)
	for _, price := range prices {
		pricesTree[commodityName].ReplaceOrInsert(*price)
	}
}

func formatFloat(num float64) string {
	s := fmt.Sprintf("%.4f", num)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

func roundToK(amount float64) float64 {
	if amount < 20000 {
		return float64(int(amount/100) * 100)
	}
	return float64(int(amount/1000) * 1000)
}

func incrementByPercentRange(amount float64, min int, max int) float64 {
	return roundToK(amount + amount*percentRange(min, max))
}

func percentRange(min int, max int) float64 {
	if min == max {
		return float64(min) * 0.01
	}
	return float64(randRange(min, max)) * 0.01
}

func randRange(min int, max int) int {
	return rand.Intn(max-min) + min
}

func taxRate(amount float64) float64 {
	if amount < 500000 {
		return 0
	} else if amount < 750000 {
		return 0.10
	} else if amount < 1000000 {
		return 0.15
	} else if amount < 1250000 {
		return 0.20
	} else if amount < 1500000 {
		return 0.25
	}
	return 0.30
}

func emitSalary(state *GeneratorState, start time.Time) {
	if start.Month() == time.April {
		state.YearlySalary = incrementByPercentRange(state.YearlySalary, 10, 15)
	}

	var salary float64 = state.YearlySalary / 12
	var company string
	if start.Year() > 2017 {
		company = "Globex"
	} else {
		company = "Acme"
	}

	tax := salary * taxRate(state.YearlySalary)
	epf := salary * 0.12
	nps := salary * 0.10
	state.EPFBalance += epf
	netSalary := salary - tax - epf - nps
	state.Balance += netSalary

	salaryAccount := fmt.Sprintf("Income:Salary:%s", company)
	emitTransaction(state.Ledger, start, "Salary", salaryAccount, "Assets:Checking", netSalary)
	emitTransaction(state.Ledger, start, "Salary EPF", salaryAccount, "Assets:Debt:EPF", epf)
	emitTransaction(state.Ledger, start, "Salary Tax", salaryAccount, "Expenses:Tax", tax)
	emitCommodityBuy(state.Ledger, start, "NPS_HDFC_E", salaryAccount, "Assets:Debt:NPS:HDFC:E", nps*0.75)
	emitCommodityBuy(state.Ledger, start, "NPS_HDFC_C", salaryAccount, "Assets:Equity:NPS:HDFC:C", nps*0.15)
	emitCommodityBuy(state.Ledger, start, "NPS_HDFC_G", salaryAccount, "Assets:Equity:NPS:HDFC:G", nps*0.10)

}

func emitExpense(state *GeneratorState, start time.Time) {
	if start.Month() == time.April {
		state.Rent = incrementByPercentRange(state.Rent, 5, 10)
	}

	emit := func(payee string, account string, amount float64, fuzz float64) {
		actualAmount := roundToK(percentRange(int(fuzz*100), 100) * amount)
		emitTransaction(state.Ledger, start, payee, "Assets:Checking", account, actualAmount)
		state.Balance -= actualAmount
	}

	emit("Rent", "Expenses:Rent", state.Rent, 1.0)
	emit("Internet", "Expenses:Utilities", 1500, 1.0)
	emit("Mobile", "Expenses:Utilities", 430, 1.0)
	emit("Shopping", "Expenses:Shopping", 3000, 0.5)
	emit("Eat out", "Expenses:Restaurants", 2500, 0.5)
	emit("Groceries", "Expenses:Food", 5000, 0.9)

	if lo.Contains([]time.Month{time.January, time.April, time.November, time.December}, start.Month()) {
		emit("Dress", "Expenses:Clothing", 5000, 0.5)
	}
}
func emitInvestment(state *GeneratorState, start time.Time) {
	if start.Month() == time.April {
		epfInterest := state.EPFBalance * 0.08
		emitTransaction(state.Ledger, start, "EPF Interest", "Income:Interest:EPF", "Assets:Debt:EPF", epfInterest)
		state.EPFBalance += epfInterest
	}

	equity1 := roundToK(state.Balance * 0.5)
	equity2 := roundToK(state.Balance * 0.2)
	debt := roundToK(state.Balance * 0.3)

	state.Balance -= equity1
	state.NiftyBalance += emitCommodityBuy(state.Ledger, start, "NIFTY", "Assets:Checking", "Assets:Equity:NIFTY", equity1)

	state.Balance -= equity2
	emitCommodityBuy(state.Ledger, start, "NIFTY_JR", "Assets:Checking", "Assets:Equity:NIFTY_JR", equity2)

	state.Balance -= debt
	emitCommodityBuy(state.Ledger, start, "ABCBF", "Assets:Checking", "Assets:Debt:ABCBF", debt)

	if start.Month() == time.March {
		units, amount := emitCommoditySell(state.Ledger, start.AddDate(0, 0, 15), "NIFTY", "Assets:Checking", "Assets:Equity:NIFTY", 75000, state.NiftyBalance)
		state.NiftyBalance += units
		state.Balance += amount
	}

}

func generateJournalFile(cwd string) {
	journalFilePath := filepath.Join(cwd, "personal.ledger")
	log.Info("Generating journal file: ", journalFilePath)
	ledgerFile, err := os.OpenFile(journalFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatal(err)
	}

	end := time.Now()
	start, err := time.Parse("02-01-2006", fmt.Sprintf("01-01-%d", START_YEAR))
	if err != nil {
		log.Fatal(err)
	}

	pricesTree = make(map[string]*btree.BTree)
	loadPrices("120716", price.MutualFund, "NIFTY", pricesTree)
	loadPrices("120684", price.MutualFund, "NIFTY_JR", pricesTree)
	loadPrices("119533", price.MutualFund, "ABCBF", pricesTree)
	loadPrices("SM008001", price.NPS, "NPS_HDFC_E", pricesTree)
	loadPrices("SM008002", price.NPS, "NPS_HDFC_C", pricesTree)
	loadPrices("SM008003", price.NPS, "NPS_HDFC_G", pricesTree)

	state := GeneratorState{Balance: 0, Ledger: ledgerFile, YearlySalary: 500000, Rent: 10000}

	for ; start.Before(end); start = start.AddDate(0, 1, 0) {
		emitSalary(&state, start)
		emitExpense(&state, start)
		emitInvestment(&state, start)
	}
}