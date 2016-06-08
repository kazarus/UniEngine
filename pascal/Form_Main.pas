unit Form_Main;

interface

uses
  System.SysUtils, System.Types, System.UITypes, System.Classes, System.Variants,
  FMX.Types, FMX.Controls, FMX.Forms, FMX.Graphics, FMX.Dialogs, FMX.TMSToolBar,
  FMX.StdCtrls, FMX.Controls.Presentation, System.Rtti, FMX.Grid, FMX.Layouts,
  FMX.TMSBaseControl, FMX.TMSGridCell, FMX.TMSGridOptions, FMX.TMSGridData,
  FMX.TMSCustomGrid, FMX.TMSGrid, FMX.TabControl;

type
  TFormMain = class(TForm)
    Tool_Main: TToolBar;
    Btnv_View: TButton;
    Btnx_2: TButton;
    spl1: TSplitter;
    Panl_1: TPanel;
    StringGrid1: TStringGrid;
    strngclmn1: TStringColumn;
    stylbk1: TStyleBook;
    Btnv_1: TButton;
    procedure FormShow(Sender: TObject);
    procedure Grid_1Click(Sender: TObject);
    procedure Btnv_ViewClick(Sender: TObject);
    procedure Btnx_2Click(Sender: TObject);
  private
  public
  end;

var
  FormMain: TFormMain;

implementation
uses
  Dialog_ListUniConfig;

{$R *.fmx}
{$R *.Windows.fmx MSWINDOWS}

procedure TFormMain.Btnv_ViewClick(Sender: TObject);
var
  Objt:TObject;
begin
  try
    Objt:=TObject.Create;
    if ViewListUniConfig(Objt)=Mrok then
    begin
      //
    end;
  finally
    FreeAndNil(Objt);
  end;
end;

procedure TFormMain.Btnx_2Click(Sender: TObject);
begin
  Application.Terminate;
end;

procedure TFormMain.FormShow(Sender: TObject);
var
  I:Integer;
begin
  self.Width :=1024;
  self.Height:=768;

  self.stylbk1.FileName:='Air.style';

  //Application.ShowException();
  {Self.Grid_1.RowCount:=1200;
  for I := 0 to 1100 do
  begin
    Self.Grid_1.Cells[1,I]:=Format('CELL%D',[I]);
    self.Grid_1.AddCheckBox(2,I,False);
  end;}

  self.StringGrid1.RowCount:=101;
  for I := 0 to 100 do
  begin
    self.StringGrid1.Cells[0,I]:='cELL'+I.ToString;
  end;
end;

procedure TFormMain.Grid_1Click(Sender: TObject);
begin
  //
end;

end.
