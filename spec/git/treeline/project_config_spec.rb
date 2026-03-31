# frozen_string_literal: true

require "tmpdir"

RSpec.describe Git::Treeline::ProjectConfig do
  let(:tmpdir) { Dir.mktmpdir }

  after { FileUtils.rm_rf(tmpdir) }

  def write_config(yaml_content)
    File.write(File.join(tmpdir, ".treeline.yml"), yaml_content)
    described_class.new(tmpdir)
  end

  describe "defaults" do
    it "provides defaults when no config file exists" do
      config = described_class.new(tmpdir)

      expect(config.database_adapter).to eq("postgresql")
      expect(config.database_template).to be_nil
      expect(config.database_pattern).to eq("{template}_{worktree}")
      expect(config.copy_files).to eq([])
      expect(config.env_template).to eq({})
      expect(config.setup_commands).to eq([])
      expect(config.env_file_target).to eq(".env.local")
      expect(config.env_file_source).to eq(".env.local")
    end

    it "uses directory basename as project name when not specified" do
      config = described_class.new(tmpdir)
      expect(config.project).to eq(File.basename(tmpdir))
    end
  end

  describe "with config file" do
    it "reads project name" do
      config = write_config("project: myapp")
      expect(config.project).to eq("myapp")
    end

    it "reads database settings" do
      config = write_config(<<~YAML)
        database:
          adapter: postgresql
          template: myapp_dev
          pattern: "{template}_{worktree}"
      YAML

      expect(config.database_adapter).to eq("postgresql")
      expect(config.database_template).to eq("myapp_dev")
    end

    it "reads env_file settings" do
      config = write_config(<<~YAML)
        env_file:
          target: .env.development.local
          source: .env.development
      YAML

      expect(config.env_file_target).to eq(".env.development.local")
      expect(config.env_file_source).to eq(".env.development")
    end

    it "merges partial config with defaults" do
      config = write_config(<<~YAML)
        project: myapp
        database:
          template: myapp_dev
      YAML

      expect(config.database_adapter).to eq("postgresql")
      expect(config.database_template).to eq("myapp_dev")
      expect(config.database_pattern).to eq("{template}_{worktree}")
    end

    it "reads copy_files" do
      config = write_config(<<~YAML)
        copy_files:
          - config/master.key
          - .env.local
      YAML

      expect(config.copy_files).to eq(["config/master.key", ".env.local"])
    end

    it "reads env template" do
      config = write_config(<<~YAML)
        env:
          PORT: "{port}"
          DATABASE_NAME: "{database}"
      YAML

      expect(config.env_template).to eq("PORT" => "{port}", "DATABASE_NAME" => "{database}")
    end
  end

  describe "#config_file_exists?" do
    it "returns false when no file" do
      config = described_class.new(tmpdir)
      expect(config.config_file_exists?).to be false
    end

    it "returns true when file exists" do
      config = write_config("project: test")
      expect(config.config_file_exists?).to be true
    end
  end
end
