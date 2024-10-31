.PHONY: db-up
db-up: ## goose postgres up
	goose -dir=./sql up
