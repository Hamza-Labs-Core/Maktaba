import click


@click.group()
@click.version_option()
def cli() -> None:
    """Maktaba: batch video transcription, subtitling, and intelligent search."""


if __name__ == "__main__":
    cli()
