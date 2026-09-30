package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/leofelipet/contexta/internal/companies"
	"github.com/leofelipet/contexta/internal/contacts"
	"github.com/leofelipet/contexta/internal/tasks"
)

func TestCompanies(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	if err := Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	suffix := time.Now().Format(time.RFC3339Nano)
	newCompany := func(name string) companies.Company {
		t.Helper()
		company, err := store.CreateCompany(ctx, companies.CreateParams{Name: name + " " + suffix})
		if err != nil {
			t.Fatal(err)
		}
		return company
	}
	newContact := func(name string) string {
		t.Helper()
		var instanceID string
		if err := store.pool.QueryRow(ctx, `
			INSERT INTO provider_instances (provider, provider_instance_id) VALUES ('uazapi', $1)
			ON CONFLICT (provider, provider_instance_id) DO UPDATE SET name = EXCLUDED.name
			RETURNING id::text`, "companies-test").Scan(&instanceID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := store.pool.QueryRow(ctx, `
			INSERT INTO contacts (provider_instance_id, provider_contact_id, name)
			VALUES ($1::uuid, $2, $3) RETURNING id::text`,
			instanceID, name+"-"+suffix+"@s.whatsapp.net", name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	t.Run("create trims, rejects duplicates ignoring case, and updates", func(t *testing.T) {
		company, err := store.CreateCompany(ctx, companies.CreateParams{Name: "  Globex " + suffix + "  ", Notes: " cliente "})
		if err != nil {
			t.Fatal(err)
		}
		if company.Name != "Globex "+suffix || company.Notes != "cliente" {
			t.Fatalf("company = %#v", company)
		}
		if _, err := store.CreateCompany(ctx, companies.CreateParams{Name: strings.ToUpper(company.Name)}); !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate err = %v, want conflict", err)
		}
		if _, err := store.CreateCompany(ctx, companies.CreateParams{Name: "   "}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("blank err = %v, want invalid argument", err)
		}
		notes := ""
		updated, err := store.UpdateCompany(ctx, company.ID, companies.UpdateParams{Notes: &notes})
		if err != nil || updated.Notes != "" || updated.Name != company.Name {
			t.Fatalf("updated = %#v, err = %v", updated, err)
		}
		page, err := store.ListCompanies(ctx, companies.ListParams{Query: "#" + company.ID})
		if err != nil || len(page.Companies) != 1 || page.Companies[0].ID != company.ID {
			t.Fatalf("list by id = %#v, err = %v", page.Companies, err)
		}
	})

	t.Run("tasks link, filter, count, and survive company deletion", func(t *testing.T) {
		company := newCompany("Initech")
		task, err := store.CreateTask(ctx, tasks.CreateParams{Title: "Proposta", CompanyID: company.ID})
		if err != nil {
			t.Fatal(err)
		}
		if task.CompanyID != company.ID || task.CompanyName != company.Name {
			t.Fatalf("task = %#v", task)
		}
		if _, err := store.CreateTask(ctx, tasks.CreateParams{Title: "x", CompanyID: "999999999"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing company err = %v, want not found", err)
		}

		page, err := store.ListTasks(ctx, tasks.ListParams{CompanyID: company.ID})
		if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != task.ID {
			t.Fatalf("tasks by company = %#v, err = %v", page.Tasks, err)
		}
		page, err = store.ListTasks(ctx, tasks.ListParams{Company: "initech " + suffix})
		if err != nil || len(page.Tasks) != 1 {
			t.Fatalf("tasks by company name = %#v, err = %v", page.Tasks, err)
		}
		got, err := store.GetCompany(ctx, company.ID)
		if err != nil || got.TaskCount != 1 || got.OpenTaskCount != 1 {
			t.Fatalf("company counts = %#v, err = %v", got, err)
		}

		empty := ""
		cleared, _, err := store.UpdateTask(ctx, task.ID, tasks.UpdateParams{CompanyID: &empty})
		if err != nil || cleared.CompanyID != "" {
			t.Fatalf("cleared = %#v, err = %v", cleared, err)
		}
		relinked, _, err := store.UpdateTask(ctx, task.ID, tasks.UpdateParams{CompanyID: &company.ID})
		if err != nil || relinked.CompanyID != company.ID {
			t.Fatalf("relinked = %#v, err = %v", relinked, err)
		}

		if err := store.DeleteCompany(ctx, company.ID); err != nil {
			t.Fatal(err)
		}
		kept, err := store.GetTask(ctx, task.ID)
		if err != nil || kept.CompanyID != "" {
			t.Fatalf("task after company delete = %#v, err = %v", kept, err)
		}
		if err := store.DeleteCompany(ctx, company.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second delete err = %v, want not found", err)
		}
	})

	t.Run("contacts attach, detach, and pass their company to new tasks", func(t *testing.T) {
		company := newCompany("Umbrella")
		contactID := newContact("Alice")

		got, err := store.AttachContactToCompany(ctx, company.ID, contactID)
		if err != nil || got.ContactCount != 1 {
			t.Fatalf("attach = %#v, err = %v", got, err)
		}
		contact, err := store.GetContact(ctx, contactID)
		if err != nil || contact.CompanyID != company.ID || contact.CompanyName != company.Name {
			t.Fatalf("contact = %#v, err = %v", contact, err)
		}
		page, err := store.ListContacts(ctx, contacts.ListParams{CompanyID: company.ID})
		if err != nil || len(page.Contacts) != 1 || page.Contacts[0].ID != contactID {
			t.Fatalf("contacts by company = %#v, err = %v", page.Contacts, err)
		}

		inherited, err := store.CreateTask(ctx, tasks.CreateParams{Title: "Ligar", ContactID: contactID})
		if err != nil || inherited.CompanyID != company.ID {
			t.Fatalf("inherited task = %#v, err = %v", inherited, err)
		}
		other := newCompany("Hooli")
		explicit, err := store.CreateTask(ctx, tasks.CreateParams{Title: "Ligar", ContactID: contactID, CompanyID: other.ID})
		if err != nil || explicit.CompanyID != other.ID {
			t.Fatalf("explicit task = %#v, err = %v", explicit, err)
		}

		if _, err := store.DetachContactFromCompany(ctx, other.ID, contactID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("detach from wrong company err = %v, want not found", err)
		}
		got, err = store.DetachContactFromCompany(ctx, company.ID, contactID)
		if err != nil || got.ContactCount != 0 {
			t.Fatalf("detach = %#v, err = %v", got, err)
		}
	})
}
